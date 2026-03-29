package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// initImage is the image used for the network-isolation init container.
const initImage = "busybox:1.37"

// KubernetesConfig holds the settings for the Kubernetes sandbox runtime.
type KubernetesConfig struct {
	Namespace    string // K8s namespace for sandbox Pods (default: "denkeeper-sandboxes")
	Kubeconfig   string // path to kubeconfig; empty = in-cluster
	RuntimeClass string // optional RuntimeClassName (e.g. "gvisor", "kata")
}

// KubernetesRuntime implements Runtime by creating ephemeral Pods in a
// Kubernetes cluster. Each sandbox is a Pod whose stdin/stdout are connected
// via `kubectl exec -i`.
type KubernetesRuntime struct {
	client       kubernetes.Interface
	namespace    string
	runtimeClass string
	logger       *slog.Logger

	mu   sync.Mutex
	pods map[string]string // plugin name → pod name

	// waitForRunning is called after Pod creation to block until the Pod
	// reaches the Running phase. Tests replace this with a no-op.
	waitForRunning func(ctx context.Context, podName string) error
}

// NewKubernetesRuntime creates a KubernetesRuntime. It verifies that kubectl
// is on PATH, builds a Kubernetes client from the config, and ensures the
// sandbox namespace exists with the correct PSA labels.
func NewKubernetesRuntime(cfg KubernetesConfig, logger *slog.Logger) (*KubernetesRuntime, error) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil, fmt.Errorf("kubectl not found on PATH: %w — kubectl is required for the kubernetes sandbox runtime", err)
	}

	restCfg, err := buildRESTConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("building kubernetes config: %w", err)
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	k := &KubernetesRuntime{
		client:       client,
		namespace:    cfg.Namespace,
		runtimeClass: cfg.RuntimeClass,
		logger:       logger,
		pods:         make(map[string]string),
	}
	k.waitForRunning = k.defaultWaitForRunning

	if err := k.ensureNamespace(context.Background()); err != nil {
		return nil, err
	}

	return k, nil
}

// newTestRuntime creates a KubernetesRuntime with an injected client for tests.
// The waitForRunning function is replaced with a no-op.
func newTestRuntime(client kubernetes.Interface, logger *slog.Logger, cfg KubernetesConfig) *KubernetesRuntime {
	ns := cfg.Namespace
	if ns == "" {
		ns = "denkeeper-sandboxes"
	}
	k := &KubernetesRuntime{
		client:       client,
		namespace:    ns,
		runtimeClass: cfg.RuntimeClass,
		logger:       logger,
		pods:         make(map[string]string),
	}
	k.waitForRunning = func(_ context.Context, _ string) error { return nil }
	return k
}

// Spawn creates an ephemeral Pod for the given plugin and returns connection
// info for kubectl exec. If a pod with the same name already exists (e.g.
// from a previous crash), it is deleted first.
func (k *KubernetesRuntime) Spawn(ctx context.Context, name string, opts SpawnOpts) (*Process, error) {
	if opts.Image == "" {
		return nil, fmt.Errorf("image is required for kubernetes sandbox")
	}

	pName := podName(name)

	// Crash recovery: delete stale pod from a previous run.
	existing, err := k.client.CoreV1().Pods(k.namespace).Get(ctx, pName, metav1.GetOptions{})
	if err == nil && existing != nil {
		k.logger.Warn("deleting stale sandbox pod from previous run",
			"plugin", name, "pod", pName, "namespace", k.namespace)
		if delErr := k.deletePod(ctx, pName); delErr != nil {
			return nil, fmt.Errorf("deleting stale pod %q: %w", pName, delErr)
		}
	}

	pod := buildPodSpec(pName, name, k.namespace, k.runtimeClass, opts)

	if _, err := k.client.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("creating sandbox pod %q: %w", pName, err)
	}

	if err := k.waitForRunning(ctx, pName); err != nil {
		// Best-effort cleanup on wait failure.
		_ = k.deletePod(ctx, pName)
		return nil, fmt.Errorf("waiting for sandbox pod %q to start: %w", pName, err)
	}

	k.mu.Lock()
	k.pods[name] = pName
	k.mu.Unlock()

	k.logger.Info("sandbox created",
		"event_type", "sandbox.created",
		"plugin", name,
		"pod", pName,
		"namespace", k.namespace,
	)

	// Build kubectl exec command for MCP stdio transport.
	args := []string{"exec", "-i", "-n", k.namespace, pName, "--"}
	if opts.Command != "" {
		args = append(args, opts.Command)
	}
	args = append(args, opts.Args...)

	return &Process{
		Command: "kubectl",
		Args:    args,
	}, nil
}

// Stop tears down the sandbox Pod for the given plugin. Idempotent: returns
// nil if the pod is already gone.
func (k *KubernetesRuntime) Stop(ctx context.Context, name string) error {
	k.mu.Lock()
	pName, ok := k.pods[name]
	if ok {
		delete(k.pods, name)
	}
	k.mu.Unlock()

	if !ok {
		return nil // not tracked, nothing to do
	}

	if err := k.deletePod(ctx, pName); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("stopping sandbox pod %q: %w", pName, err)
	}

	k.logger.Info("sandbox destroyed",
		"event_type", "sandbox.destroyed",
		"plugin", name,
		"pod", pName,
		"namespace", k.namespace,
	)
	return nil
}

// Close deletes all tracked sandbox Pods. Best-effort: logs errors but does
// not stop on the first failure.
func (k *KubernetesRuntime) Close() error {
	k.mu.Lock()
	pods := make(map[string]string, len(k.pods))
	for name, pName := range k.pods {
		pods[name] = pName
	}
	k.pods = make(map[string]string)
	k.mu.Unlock()

	var firstErr error
	for name, pName := range pods {
		if err := k.deletePod(context.Background(), pName); err != nil && !errors.IsNotFound(err) {
			k.logger.Error("failed to delete sandbox pod on close",
				"plugin", name, "pod", pName, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ensureNamespace creates the sandbox namespace if it does not exist, or
// updates its PSA labels if it does.
func (k *KubernetesRuntime) ensureNamespace(ctx context.Context) error {
	labels := map[string]string{
		"pod-security.kubernetes.io/enforce":         "baseline",
		"pod-security.kubernetes.io/enforce-version": "latest",
		"pod-security.kubernetes.io/audit":           "restricted",
		"pod-security.kubernetes.io/warn":            "restricted",
		"app.kubernetes.io/managed-by":               "denkeeper",
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   k.namespace,
			Labels: labels,
		},
	}

	_, err := k.client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err == nil {
		k.logger.Info("sandbox namespace created", "namespace", k.namespace)
		return nil
	}

	if !errors.IsAlreadyExists(err) {
		return fmt.Errorf("creating sandbox namespace %q: %w", k.namespace, err)
	}

	// Namespace exists — update labels.
	existing, getErr := k.client.CoreV1().Namespaces().Get(ctx, k.namespace, metav1.GetOptions{})
	if getErr != nil {
		return fmt.Errorf("getting sandbox namespace %q: %w", k.namespace, getErr)
	}

	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	for key, val := range labels {
		existing.Labels[key] = val
	}

	if _, updErr := k.client.CoreV1().Namespaces().Update(ctx, existing, metav1.UpdateOptions{}); updErr != nil {
		return fmt.Errorf("updating sandbox namespace labels: %w", updErr)
	}

	k.logger.Info("sandbox namespace labels updated", "namespace", k.namespace)
	return nil
}

// deletePod deletes a pod with a short grace period.
func (k *KubernetesRuntime) deletePod(ctx context.Context, name string) error {
	grace := int64(5)
	return k.client.CoreV1().Pods(k.namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &grace,
	})
}

// defaultWaitForRunning watches the pod until it reaches Running or fails.
func (k *KubernetesRuntime) defaultWaitForRunning(ctx context.Context, name string) error {
	watcher, err := k.client.CoreV1().Pods(k.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + name,
	})
	if err != nil {
		return fmt.Errorf("watching pod %q: %w", name, err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled waiting for pod %q: %w", name, ctx.Err())
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return fmt.Errorf("watch channel closed for pod %q", name)
			}
			if event.Type == watch.Deleted {
				return fmt.Errorf("pod %q was deleted while waiting for it to start", name)
			}
			pod, isPod := event.Object.(*corev1.Pod)
			if !isPod {
				continue
			}
			switch pod.Status.Phase {
			case corev1.PodRunning:
				return nil
			case corev1.PodFailed, corev1.PodSucceeded:
				return fmt.Errorf("pod %q entered phase %s instead of Running", name, pod.Status.Phase)
			}
		}
	}
}

// buildPodSpec constructs the Pod object for a sandbox.
func buildPodSpec(podName, pluginName, namespace, runtimeClass string, opts SpawnOpts) *corev1.Pod {
	trueVal := true
	falseVal := false

	// Main container.
	container := corev1.Container{
		Name:  "mcp-server",
		Image: opts.Image,
		Stdin: true,
		SecurityContext: &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &trueVal,
			AllowPrivilegeEscalation: &falseVal,
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
	}

	// Command/args override.
	if opts.Command != "" {
		container.Command = []string{opts.Command}
	}
	if len(opts.Args) > 0 {
		container.Args = opts.Args
	}

	// Environment variables.
	for k, v := range opts.Env {
		container.Env = append(container.Env, corev1.EnvVar{Name: k, Value: v})
	}

	// Resource limits (requests = limits for Guaranteed QoS).
	limits := corev1.ResourceList{}
	if opts.MemoryLimit != "" {
		if q, err := resource.ParseQuantity(opts.MemoryLimit); err == nil {
			limits[corev1.ResourceMemory] = q
		}
	}
	if opts.CPULimit != "" {
		if q, err := resource.ParseQuantity(opts.CPULimit); err == nil {
			limits[corev1.ResourceCPU] = q
		}
	}
	if len(limits) > 0 {
		container.Resources = corev1.ResourceRequirements{
			Limits:   limits,
			Requests: limits.DeepCopy(),
		}
	}

	// Volume mounts from opts.Volumes.
	var volumes []corev1.Volume
	var volumeMounts []corev1.VolumeMount
	for i, v := range opts.Volumes {
		volName := fmt.Sprintf("vol-%d", i)
		hostPath, mountPath, readOnly := parseVolumeSpec(v)
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: hostPath},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: mountPath,
			ReadOnly:  readOnly,
		})
	}
	container.VolumeMounts = volumeMounts

	// Init container for network isolation.
	script := iptablesScript(opts.Network)
	initContainer := corev1.Container{
		Name:    "net-init",
		Image:   initImage,
		Command: []string{"/bin/sh", "-c", script},
		SecurityContext: &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Add:  []corev1.Capability{"NET_ADMIN"},
				Drop: []corev1.Capability{"ALL"},
			},
			ReadOnlyRootFilesystem:   &trueVal,
			RunAsNonRoot:             &falseVal, // needs root for iptables
			AllowPrivilegeEscalation: &falseVal,
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "denkeeper",
				"app.kubernetes.io/component":  "sandbox",
				"denkeeper.dev/plugin":         pluginName,
			},
		},
		Spec: corev1.PodSpec{
			InitContainers:               []corev1.Container{initContainer},
			Containers:                   []corev1.Container{container},
			Volumes:                      volumes,
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &falseVal,
		},
	}

	if runtimeClass != "" {
		pod.Spec.RuntimeClassName = &runtimeClass
	}

	return pod
}

// iptablesScript returns the shell script for the network-isolation init container.
func iptablesScript(policy NetworkPolicy) string {
	switch policy {
	case NetworkEgress:
		return "iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT && " +
			"iptables -A INPUT -i lo -j ACCEPT && " +
			"iptables -A INPUT -j DROP"
	case NetworkFull:
		return "echo 'network=full, no restrictions'"
	default: // NetworkNone or empty
		return "iptables -A INPUT -i lo -j ACCEPT && " +
			"iptables -A INPUT -j DROP && " +
			"iptables -A OUTPUT -o lo -j ACCEPT && " +
			"iptables -A OUTPUT -j DROP"
	}
}

// buildRESTConfig creates a Kubernetes REST config from a kubeconfig path,
// or uses in-cluster config if the path is empty.
func buildRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

// podName generates a DNS-1123 compliant pod name from a plugin name.
// Max 63 characters, lowercase alphanumeric and hyphens only.
func podName(name string) string {
	const prefix = "denkeeper-"
	const maxLen = 63

	// Lowercase and replace invalid characters with hyphens.
	s := strings.ToLower(name)
	s = invalidDNSChars.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens.
	s = strings.Trim(s, "-")

	result := prefix + s
	if len(result) > maxLen {
		result = result[:maxLen]
	}
	// Trim trailing hyphen after truncation.
	result = strings.TrimRight(result, "-")
	return result
}

var invalidDNSChars = regexp.MustCompile(`[^a-z0-9-]`)

// parseVolumeSpec splits a Docker-style volume spec "host:container[:ro]"
// into its components.
func parseVolumeSpec(spec string) (hostPath, mountPath string, readOnly bool) {
	parts := strings.SplitN(spec, ":", 3)
	switch len(parts) {
	case 1:
		return parts[0], parts[0], false
	case 2:
		return parts[0], parts[1], false
	default:
		return parts[0], parts[1], parts[2] == "ro"
	}
}
