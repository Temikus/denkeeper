package sandbox

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestK8sRuntime(runtimeClass string) *KubernetesRuntime {
	client := fake.NewSimpleClientset()
	return newTestRuntime(client, slog.Default(), KubernetesConfig{
		Namespace:    "test-sandboxes",
		RuntimeClass: runtimeClass,
	})
}

// spawnTestPod creates a runtime with the given runtimeClass, spawns a plugin
// with full options, and returns the created Pod.
func spawnTestPod(t *testing.T, runtimeClass string) *corev1.Pod {
	t.Helper()
	rt := newTestK8sRuntime(runtimeClass)

	_, err := rt.Spawn(context.Background(), "my-plugin", SpawnOpts{
		Image:       "mcp-server:v1",
		Command:     "/usr/bin/serve",
		Args:        []string{"--verbose"},
		Env:         map[string]string{"API_KEY": "secret"},
		MemoryLimit: "256Mi",
		CPULimit:    "500m",
		Network:     NetworkNone,
		Volumes:     []string{"/data:/mnt/data:ro"},
	})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	pod, err := rt.client.CoreV1().Pods("test-sandboxes").Get(
		context.Background(), "denkeeper-my-plugin", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting pod: %v", err)
	}
	return pod
}

func TestKubernetesRuntime_Spawn_PodMetadata(t *testing.T) {
	pod := spawnTestPod(t, "gvisor")

	if pod.Labels["app.kubernetes.io/managed-by"] != "denkeeper" {
		t.Error("missing managed-by label")
	}
	if pod.Labels["denkeeper.dev/plugin"] != "my-plugin" {
		t.Error("missing plugin label")
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
		t.Error("expected RuntimeClassName=gvisor")
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %v, want Never", pod.Spec.RestartPolicy)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Error("expected AutomountServiceAccountToken=false")
	}
}

func TestKubernetesRuntime_Spawn_InitContainer(t *testing.T) {
	pod := spawnTestPod(t, "")

	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(pod.Spec.InitContainers))
	}
	initC := pod.Spec.InitContainers[0]
	if initC.Name != "net-init" {
		t.Errorf("init container name = %q, want net-init", initC.Name)
	}
	if initC.Image != initImage {
		t.Errorf("init container image = %q, want %q", initC.Image, initImage)
	}
	hasNetAdmin := false
	for _, cap := range initC.SecurityContext.Capabilities.Add {
		if cap == "NET_ADMIN" {
			hasNetAdmin = true
		}
	}
	if !hasNetAdmin {
		t.Error("init container missing NET_ADMIN capability")
	}
}

func TestKubernetesRuntime_Spawn_SecurityContext(t *testing.T) {
	pod := spawnTestPod(t, "")

	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(pod.Spec.Containers))
	}
	c := pod.Spec.Containers[0]
	if c.Image != "mcp-server:v1" {
		t.Errorf("container image = %q, want mcp-server:v1", c.Image)
	}
	if !c.Stdin {
		t.Error("expected Stdin=true")
	}
	sc := c.SecurityContext
	if sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("expected ReadOnlyRootFilesystem=true")
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("expected RunAsNonRoot=true")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("expected AllowPrivilegeEscalation=false")
	}
	if len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Error("expected capabilities drop ALL")
	}
}

func TestKubernetesRuntime_Spawn_CommandAndResources(t *testing.T) {
	pod := spawnTestPod(t, "")
	c := pod.Spec.Containers[0]

	if len(c.Command) != 1 || c.Command[0] != "/usr/bin/serve" {
		t.Errorf("Command = %v, want [/usr/bin/serve]", c.Command)
	}
	if len(c.Args) != 1 || c.Args[0] != "--verbose" {
		t.Errorf("Args = %v, want [--verbose]", c.Args)
	}

	foundEnv := false
	for _, e := range c.Env {
		if e.Name == "API_KEY" && e.Value == "secret" {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Error("missing API_KEY env var")
	}

	mem := c.Resources.Limits.Memory()
	if mem == nil || mem.String() != "256Mi" {
		t.Errorf("memory limit = %v, want 256Mi", mem)
	}
	cpu := c.Resources.Limits.Cpu()
	if cpu == nil || cpu.String() != "500m" {
		t.Errorf("cpu limit = %v, want 500m", cpu)
	}

	if len(c.VolumeMounts) != 1 {
		t.Fatalf("expected 1 volume mount, got %d", len(c.VolumeMounts))
	}
	if c.VolumeMounts[0].MountPath != "/mnt/data" || !c.VolumeMounts[0].ReadOnly {
		t.Errorf("volume mount = %+v, want /mnt/data:ro", c.VolumeMounts[0])
	}
}

func TestKubernetesRuntime_Spawn_TmpfsVolumes(t *testing.T) {
	rt := newTestK8sRuntime("")

	_, err := rt.Spawn(context.Background(), "tmpfs-plugin", SpawnOpts{
		Image: "img:v1",
		Tmpfs: []string{"/tmp:size=64m", "/run"},
	})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	pod, err := rt.client.CoreV1().Pods("test-sandboxes").Get(
		context.Background(), "denkeeper-tmpfs-plugin", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting pod: %v", err)
	}

	// Should have 2 tmpfs volumes.
	var tmpfsVols int
	for _, v := range pod.Spec.Volumes {
		if strings.HasPrefix(v.Name, "tmpfs-") && v.VolumeSource.EmptyDir != nil &&
			v.VolumeSource.EmptyDir.Medium == corev1.StorageMediumMemory {
			tmpfsVols++
		}
	}
	if tmpfsVols != 2 {
		t.Errorf("expected 2 tmpfs volumes, got %d", tmpfsVols)
	}

	// Check size limit on first tmpfs.
	for _, v := range pod.Spec.Volumes {
		if v.Name == "tmpfs-0" {
			if v.VolumeSource.EmptyDir.SizeLimit == nil {
				t.Error("expected sizeLimit on tmpfs-0")
			} else if v.VolumeSource.EmptyDir.SizeLimit.String() != "64M" {
				t.Errorf("tmpfs-0 sizeLimit = %s, want 64M", v.VolumeSource.EmptyDir.SizeLimit.String())
			}
		}
		if v.Name == "tmpfs-1" && v.VolumeSource.EmptyDir.SizeLimit != nil {
			t.Error("tmpfs-1 should not have a sizeLimit")
		}
	}

	// Check volume mounts.
	c := pod.Spec.Containers[0]
	mountPaths := make(map[string]bool)
	for _, m := range c.VolumeMounts {
		mountPaths[m.MountPath] = true
	}
	if !mountPaths["/tmp"] {
		t.Error("missing /tmp mount")
	}
	if !mountPaths["/run"] {
		t.Error("missing /run mount")
	}
}

func TestKubernetesRuntime_Spawn_ShmSize(t *testing.T) {
	rt := newTestK8sRuntime("")

	_, err := rt.Spawn(context.Background(), "shm-plugin", SpawnOpts{
		Image:   "img:v1",
		ShmSize: "128M",
	})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	pod, err := rt.client.CoreV1().Pods("test-sandboxes").Get(
		context.Background(), "denkeeper-shm-plugin", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting pod: %v", err)
	}

	// Should have a dshm volume.
	var found bool
	for _, v := range pod.Spec.Volumes {
		if v.Name == "dshm" {
			found = true
			if v.VolumeSource.EmptyDir == nil {
				t.Fatal("dshm volume is not emptyDir")
			}
			if v.VolumeSource.EmptyDir.Medium != corev1.StorageMediumMemory {
				t.Error("dshm medium should be Memory")
			}
			if v.VolumeSource.EmptyDir.SizeLimit == nil || v.VolumeSource.EmptyDir.SizeLimit.String() != "128M" {
				t.Errorf("dshm sizeLimit = %v, want 128M", v.VolumeSource.EmptyDir.SizeLimit)
			}
		}
	}
	if !found {
		t.Error("missing dshm volume")
	}

	// Check /dev/shm mount.
	c := pod.Spec.Containers[0]
	var shmMounted bool
	for _, m := range c.VolumeMounts {
		if m.MountPath == "/dev/shm" && m.Name == "dshm" {
			shmMounted = true
		}
	}
	if !shmMounted {
		t.Error("missing /dev/shm volume mount")
	}
}

func TestKubernetesRuntime_Spawn_ReturnedProcess(t *testing.T) {
	rt := newTestK8sRuntime("")

	proc, err := rt.Spawn(context.Background(), "test-plugin", SpawnOpts{
		Image:   "img:v1",
		Command: "/bin/server",
		Args:    []string{"--port", "8080"},
	})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	if proc.Command != "kubectl" {
		t.Errorf("Command = %q, want kubectl", proc.Command)
	}

	expected := []string{"exec", "-i", "-n", "test-sandboxes", "denkeeper-test-plugin", "--", "/bin/server", "--port", "8080"}
	if len(proc.Args) != len(expected) {
		t.Fatalf("Args length = %d, want %d\ngot:  %v\nwant: %v", len(proc.Args), len(expected), proc.Args, expected)
	}
	for i := range expected {
		if proc.Args[i] != expected[i] {
			t.Errorf("Args[%d] = %q, want %q", i, proc.Args[i], expected[i])
		}
	}
}

func TestKubernetesRuntime_Spawn_MissingImage(t *testing.T) {
	rt := newTestK8sRuntime("")

	_, err := rt.Spawn(context.Background(), "bad", SpawnOpts{})
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestKubernetesRuntime_Spawn_NetworkPolicies(t *testing.T) {
	tests := []struct {
		policy   NetworkPolicy
		contains string
	}{
		{NetworkNone, "OUTPUT -j DROP"},
		{"", "OUTPUT -j DROP"}, // default = none
		{NetworkEgress, "ESTABLISHED,RELATED"},
		{NetworkFull, "no restrictions"},
	}

	for _, tt := range tests {
		script := iptablesScript(tt.policy)
		if !strings.Contains(script, tt.contains) {
			t.Errorf("iptablesScript(%q) = %q, want to contain %q", tt.policy, script, tt.contains)
		}
	}
}

func TestKubernetesRuntime_Spawn_ExistingPod(t *testing.T) {
	rt := newTestK8sRuntime("")

	// Create a stale pod manually.
	stalePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "denkeeper-stale",
			Namespace: "test-sandboxes",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "old:v1"}},
		},
	}
	_, err := rt.client.CoreV1().Pods("test-sandboxes").Create(
		context.Background(), stalePod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("creating stale pod: %v", err)
	}

	// Spawn should delete the stale pod and create a new one.
	_, err = rt.Spawn(context.Background(), "stale", SpawnOpts{
		Image: "new:v1",
	})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	// Verify the new pod exists.
	pod, err := rt.client.CoreV1().Pods("test-sandboxes").Get(
		context.Background(), "denkeeper-stale", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting pod: %v", err)
	}
	if pod.Spec.Containers[0].Image != "new:v1" {
		t.Errorf("pod image = %q, want new:v1", pod.Spec.Containers[0].Image)
	}
}

func TestKubernetesRuntime_Stop_DeletesPod(t *testing.T) {
	rt := newTestK8sRuntime("")

	_, err := rt.Spawn(context.Background(), "to-stop", SpawnOpts{
		Image: "img:v1",
	})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	if err := rt.Stop(context.Background(), "to-stop"); err != nil {
		t.Fatalf("Stop error: %v", err)
	}

	// Pod should be deleted.
	_, err = rt.client.CoreV1().Pods("test-sandboxes").Get(
		context.Background(), "denkeeper-to-stop", metav1.GetOptions{})
	if err == nil {
		t.Error("expected pod to be deleted")
	}

	// Should be removed from tracking.
	rt.mu.Lock()
	_, tracked := rt.pods["to-stop"]
	rt.mu.Unlock()
	if tracked {
		t.Error("pod still tracked after Stop")
	}
}

func TestKubernetesRuntime_Stop_NotFound(t *testing.T) {
	rt := newTestK8sRuntime("")

	// Stop on unknown name should not error.
	if err := rt.Stop(context.Background(), "nonexistent"); err != nil {
		t.Errorf("Stop returned error: %v", err)
	}
}

func TestKubernetesRuntime_Close_DeletesAll(t *testing.T) {
	rt := newTestK8sRuntime("")

	for _, name := range []string{"plugin-a", "plugin-b"} {
		if _, err := rt.Spawn(context.Background(), name, SpawnOpts{Image: "img:v1"}); err != nil {
			t.Fatalf("Spawn %q: %v", name, err)
		}
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	// Both pods should be deleted.
	pods, err := rt.client.CoreV1().Pods("test-sandboxes").List(
		context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing pods: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Errorf("expected 0 pods after Close, got %d", len(pods.Items))
	}

	// Tracking map should be empty.
	rt.mu.Lock()
	n := len(rt.pods)
	rt.mu.Unlock()
	if n != 0 {
		t.Errorf("expected 0 tracked pods after Close, got %d", n)
	}
}

func TestKubernetesRuntime_EnsureNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	rt := newTestRuntime(client, slog.Default(), KubernetesConfig{
		Namespace: "my-sandboxes",
	})

	if err := rt.ensureNamespace(context.Background()); err != nil {
		t.Fatalf("ensureNamespace error: %v", err)
	}

	ns, err := client.CoreV1().Namespaces().Get(context.Background(), "my-sandboxes", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting namespace: %v", err)
	}

	wantLabels := map[string]string{
		"pod-security.kubernetes.io/enforce":         "baseline",
		"pod-security.kubernetes.io/enforce-version": "latest",
		"pod-security.kubernetes.io/audit":           "restricted",
		"pod-security.kubernetes.io/warn":            "restricted",
		"app.kubernetes.io/managed-by":               "denkeeper",
	}
	for k, v := range wantLabels {
		if ns.Labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, ns.Labels[k], v)
		}
	}
}

func TestKubernetesRuntime_EnsureNamespace_AlreadyExists(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Pre-create the namespace without PSA labels.
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "existing-ns",
			Labels: map[string]string{"some-label": "value"},
		},
	}
	if _, err := client.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating namespace: %v", err)
	}

	rt := newTestRuntime(client, slog.Default(), KubernetesConfig{
		Namespace: "existing-ns",
	})

	if err := rt.ensureNamespace(context.Background()); err != nil {
		t.Fatalf("ensureNamespace error: %v", err)
	}

	updated, err := client.CoreV1().Namespaces().Get(context.Background(), "existing-ns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting namespace: %v", err)
	}

	// Original label preserved.
	if updated.Labels["some-label"] != "value" {
		t.Error("original label lost")
	}
	// PSA labels added.
	if updated.Labels["pod-security.kubernetes.io/enforce"] != "baseline" {
		t.Error("PSA enforce label not added")
	}
}

func TestPodName_Sanitization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "denkeeper-simple"},
		{"My_Plugin", "denkeeper-my-plugin"},
		{"UPPER.CASE", "denkeeper-upper-case"},
		{"with spaces", "denkeeper-with-spaces"},
		{"--leading-hyphens--", "denkeeper-leading-hyphens"},
		{strings.Repeat("a", 100), "denkeeper-" + strings.Repeat("a", 53)},
	}

	for _, tt := range tests {
		got := podName(tt.input)
		if got != tt.want {
			t.Errorf("podName(%q) = %q, want %q", tt.input, got, tt.want)
		}
		// Must be valid DNS-1123 label.
		if len(got) > 63 {
			t.Errorf("podName(%q) length %d > 63", tt.input, len(got))
		}
	}
}

func TestParseTmpfsSpec(t *testing.T) {
	tests := []struct {
		spec      string
		wantPath  string
		wantLimit string
	}{
		{"/tmp", "/tmp", ""},
		{"/tmp:size=64m", "/tmp", "64M"},
		{"/run:size=128M,noexec", "/run", "128M"},
		{"/cache:noexec,size=1g", "/cache", "1G"},
		{"/var:rw", "/var", ""},
	}

	for _, tt := range tests {
		path, limit := parseTmpfsSpec(tt.spec)
		if path != tt.wantPath {
			t.Errorf("parseTmpfsSpec(%q) path = %q, want %q", tt.spec, path, tt.wantPath)
		}
		if limit != tt.wantLimit {
			t.Errorf("parseTmpfsSpec(%q) limit = %q, want %q", tt.spec, limit, tt.wantLimit)
		}
	}
}

func TestKubernetesRuntime_Spawn_NoRuntimeClass(t *testing.T) {
	rt := newTestK8sRuntime("")

	_, err := rt.Spawn(context.Background(), "no-rc", SpawnOpts{Image: "img:v1"})
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	pod, err := rt.client.CoreV1().Pods("test-sandboxes").Get(
		context.Background(), "denkeeper-no-rc", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting pod: %v", err)
	}

	if pod.Spec.RuntimeClassName != nil {
		t.Errorf("expected nil RuntimeClassName, got %q", *pod.Spec.RuntimeClassName)
	}
}
