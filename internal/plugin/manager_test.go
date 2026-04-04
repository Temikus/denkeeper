package plugin

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/sandbox"
	"github.com/Temikus/denkeeper/internal/security"
)

// mockToolRegistrar records RegisterServer calls and can simulate failures.
type mockToolRegistrar struct {
	registered []registeredServer
	failNames  map[string]bool
}

type registeredServer struct {
	name    string
	command string
	args    []string
}

func (m *mockToolRegistrar) RegisterServer(_ context.Context, name string, cfg config.ToolConfig) error {
	if m.failNames[name] {
		return errors.New("mock: simulated failure")
	}
	m.registered = append(m.registered, registeredServer{name: name, command: cfg.Command, args: cfg.Args})
	return nil
}

func newTestManager() *Manager {
	return NewManager(slog.Default(), nil, nil)
}

func TestLoad_SubprocessWithToolsCapability_Succeeds(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"my-plugin": {Type: "subprocess", Command: "/usr/bin/my-plugin", Capabilities: []string{"tools"}},
	}

	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if mgr.Count() != 1 {
		t.Fatalf("expected Count() == 1, got %d", mgr.Count())
	}
}

func TestLoad_DockerType_Succeeds(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"docker-plugin": {
			Type:         "docker",
			Image:        "myregistry/mcp-plugin:v1",
			MemoryLimit:  "256m",
			CPULimit:     "0.5",
			Network:      "none",
			Capabilities: []string{"tools"},
		},
	}

	// This test will fail if Docker is not on PATH, which is expected in CI.
	// We override checkDockerAvailable by testing Load with a subprocess-only variant
	// or accept the failure gracefully.
	err := mgr.Load(plugins, nil)
	if err != nil {
		if strings.Contains(err.Error(), "docker CLI not found") {
			t.Skip("docker not available, skipping")
		}
		t.Fatalf("expected no error, got: %v", err)
	}
	if mgr.Count() != 1 {
		t.Fatalf("expected Count() == 1, got %d", mgr.Count())
	}
	p := mgr.plugins[0]
	if p.Image != "myregistry/mcp-plugin:v1" {
		t.Errorf("expected image 'myregistry/mcp-plugin:v1', got %q", p.Image)
	}
	if p.Network != "none" {
		t.Errorf("expected network 'none', got %q", p.Network)
	}
}

func TestLoad_DockerType_MissingImage_ReturnsError(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"bad-docker": {Type: "docker", Command: "something", Capabilities: []string{"tools"}},
	}

	err := mgr.Load(plugins, nil)
	if err == nil {
		t.Fatal("expected error for missing image, got nil")
	}
	if !strings.Contains(err.Error(), "image is required") {
		t.Errorf("expected error about missing image, got: %s", err.Error())
	}
}

func TestLoad_DockerType_DefaultsNetworkToNone(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"docker-plugin": {
			Type:         "docker",
			Image:        "test:latest",
			Capabilities: []string{"tools"},
			// Network intentionally omitted.
		},
	}

	err := mgr.Load(plugins, nil)
	if err != nil {
		if strings.Contains(err.Error(), "docker CLI not found") {
			t.Skip("docker not available, skipping")
		}
		t.Fatalf("expected no error, got: %v", err)
	}
	if mgr.plugins[0].Network != "none" {
		t.Errorf("expected default network 'none', got %q", mgr.plugins[0].Network)
	}
}

func TestLoad_NameCollisionWithTool_ReturnsError(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"web-search": {Type: "subprocess", Command: "/usr/bin/search", Capabilities: []string{"tools"}},
	}
	existing := map[string]bool{"web-search": true}

	err := mgr.Load(plugins, existing)
	if err == nil {
		t.Fatal("expected error for name collision, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "conflicts with existing tool") {
		t.Errorf("expected error to contain 'conflicts with existing tool', got: %s", got)
	}
}

func TestLoad_MissingCommand_ReturnsError(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"bad-plugin": {Type: "subprocess", Command: "", Capabilities: []string{"tools"}},
	}

	err := mgr.Load(plugins, nil)
	if err == nil {
		t.Fatal("expected error for missing command, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "command is required") {
		t.Errorf("expected error to contain 'command is required', got: %s", got)
	}
}

func TestLoad_UnknownCapability_LogsWarnAndSucceeds(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"mixed-plugin": {Type: "subprocess", Command: "/usr/bin/mixed", Capabilities: []string{"tools", "adapter"}},
	}

	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("expected no error for unknown capability, got: %v", err)
	}
	if mgr.Count() != 1 {
		t.Fatalf("expected Count() == 1, got %d", mgr.Count())
	}
	// Verify only the known capability was retained.
	if len(mgr.plugins[0].Capabilities) != 1 || mgr.plugins[0].Capabilities[0] != CapabilityTools {
		t.Errorf("expected only 'tools' capability retained, got: %v", mgr.plugins[0].Capabilities)
	}
}

func TestLoad_EmptyPlugins_NoError(t *testing.T) {
	mgr := newTestManager()

	if err := mgr.Load(nil, nil); err != nil {
		t.Fatalf("expected no error for empty plugins, got: %v", err)
	}
	if mgr.Count() != 0 {
		t.Fatalf("expected Count() == 0, got %d", mgr.Count())
	}
}

func TestStart_ToolsCapability_RegistersWithToolManager(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"plugin-a": {Type: "subprocess", Command: "/usr/bin/plugin-a", Capabilities: []string{"tools"}},
		"plugin-b": {Type: "subprocess", Command: "/usr/bin/plugin-b", Capabilities: []string{"tools"}},
	}
	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}

	mock := &mockToolRegistrar{}
	if err := mgr.Start(context.Background(), mock); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(mock.registered) != 2 {
		t.Errorf("expected 2 registrations, got %d: %v", len(mock.registered), mock.registered)
	}
}

func newDockerTestManager() *Manager {
	// Use a real DockerRuntime bypassing the PATH check (Spawn doesn't need docker).
	return NewManager(slog.Default(), nil, &sandbox.DockerRuntime{})
}

func TestStart_DockerPlugin_RegistersAsDockerRun(t *testing.T) {
	mgr := newDockerTestManager()
	// Manually load a Docker plugin (skip Load to avoid Docker CLI check).
	mgr.plugins = []Plugin{
		{
			Name:         "docker-mcp",
			Type:         TypeDocker,
			Image:        "mcp-plugin:latest",
			MemoryLimit:  "128m",
			CPULimit:     "0.5",
			Network:      "none",
			Capabilities: []Capability{CapabilityTools},
			Env:          map[string]string{"API_KEY": "test123"},
		},
	}

	mock := &mockToolRegistrar{}
	if err := mgr.Start(context.Background(), mock); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(mock.registered) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(mock.registered))
	}

	reg := mock.registered[0]
	if reg.command != "docker" {
		t.Errorf("expected command 'docker', got %q", reg.command)
	}

	// Verify key docker run flags are present in args.
	argsStr := strings.Join(reg.args, " ")
	for _, expected := range []string{"run", "--rm", "-i", "--network none", "--memory 128m", "--cpus 0.5", "--cap-drop ALL", "--read-only", "mcp-plugin:latest"} {
		if !strings.Contains(argsStr, expected) {
			t.Errorf("expected args to contain %q, got: %s", expected, argsStr)
		}
	}
	if !strings.Contains(argsStr, "-e API_KEY=test123") {
		t.Errorf("expected args to contain env var, got: %s", argsStr)
	}
}

func TestStart_DockerPlugin_WithCommandOverride(t *testing.T) {
	mgr := newDockerTestManager()
	mgr.plugins = []Plugin{
		{
			Name:         "docker-custom",
			Type:         TypeDocker,
			Image:        "mcp-plugin:latest",
			Command:      "/usr/local/bin/custom-entrypoint",
			Args:         []string{"--verbose"},
			Network:      "bridge",
			Capabilities: []Capability{CapabilityTools},
		},
	}

	mock := &mockToolRegistrar{}
	if err := mgr.Start(context.Background(), mock); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	reg := mock.registered[0]
	argsStr := strings.Join(reg.args, " ")
	if !strings.Contains(argsStr, "--network bridge") {
		t.Errorf("expected network bridge, got: %s", argsStr)
	}
	if !strings.Contains(argsStr, "/usr/local/bin/custom-entrypoint") {
		t.Errorf("expected command override in args, got: %s", argsStr)
	}
	if !strings.Contains(argsStr, "--verbose") {
		t.Errorf("expected plugin args, got: %s", argsStr)
	}
}

func TestStart_PluginFailure_LogsErrorAndContinues(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"plugin-fail": {Type: "subprocess", Command: "/usr/bin/fail", Capabilities: []string{"tools"}},
		"plugin-ok":   {Type: "subprocess", Command: "/usr/bin/ok", Capabilities: []string{"tools"}},
	}
	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}

	mock := &mockToolRegistrar{failNames: map[string]bool{"plugin-fail": true}}
	err := mgr.Start(context.Background(), mock)
	if err == nil {
		t.Fatal("expected error from failing plugin, got nil")
	}
	// The successful plugin should still be registered.
	if len(mock.registered) != 1 || mock.registered[0].name != "plugin-ok" {
		t.Errorf("expected 'plugin-ok' to be registered, got: %v", mock.registered)
	}
}

func TestStart_NoEffectiveCapability_DoesNotRegister(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		// Only unknown capabilities — "tools" capability will be filtered out by Load.
		"adapter-plugin": {Type: "subprocess", Command: "/usr/bin/adapter", Capabilities: []string{"adapter"}},
	}
	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}

	mock := &mockToolRegistrar{}
	if err := mgr.Start(context.Background(), mock); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(mock.registered) != 0 {
		t.Errorf("expected no registrations, got: %v", mock.registered)
	}
}

// --- Signature verification tests ---

func TestLoad_SignedPlugin_Verified(t *testing.T) {
	// Create a fake plugin binary and sign it.
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "signed-plugin")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\necho hello"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pub, priv, err := security.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if err := security.SignFile(priv, pluginPath); err != nil {
		t.Fatalf("SignFile: %v", err)
	}

	opts := &VerifyOpts{
		TrustedKeys:   []ed25519.PublicKey{pub},
		AllowUnsigned: false,
	}
	mgr := NewManager(slog.Default(), opts, nil)
	plugins := map[string]config.PluginConfig{
		"signed": {Type: "subprocess", Command: pluginPath, Capabilities: []string{"tools"}},
	}

	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("Load should succeed for signed plugin: %v", err)
	}
}

func TestLoad_UnsignedPlugin_RejectedWhenRequired(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "unsigned-plugin")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\necho hello"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pub, _, _ := security.GenerateKeyPair()
	opts := &VerifyOpts{
		TrustedKeys:   []ed25519.PublicKey{pub},
		AllowUnsigned: false,
	}
	mgr := NewManager(slog.Default(), opts, nil)
	plugins := map[string]config.PluginConfig{
		"unsigned": {Type: "subprocess", Command: pluginPath, Capabilities: []string{"tools"}},
	}

	err := mgr.Load(plugins, nil)
	if err == nil {
		t.Fatal("expected error for unsigned plugin when allow_unsigned=false")
	}
	if !strings.Contains(err.Error(), "signature verification failed") {
		t.Errorf("expected signature verification error, got: %v", err)
	}
}

func TestLoad_UnsignedPlugin_AllowedWhenPermitted(t *testing.T) {
	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "unsigned-plugin")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\necho hello"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	pub, _, _ := security.GenerateKeyPair()
	opts := &VerifyOpts{
		TrustedKeys:   []ed25519.PublicKey{pub},
		AllowUnsigned: true,
	}
	mgr := NewManager(slog.Default(), opts, nil)
	plugins := map[string]config.PluginConfig{
		"unsigned": {Type: "subprocess", Command: pluginPath, Capabilities: []string{"tools"}},
	}

	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("Load should succeed when allow_unsigned=true: %v", err)
	}
}

func TestLoad_NoVerifyOpts_SkipsVerification(t *testing.T) {
	mgr := NewManager(slog.Default(), nil, nil)
	plugins := map[string]config.PluginConfig{
		"unverified": {Type: "subprocess", Command: "/usr/bin/whatever", Capabilities: []string{"tools"}},
	}

	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("Load should succeed without verify opts: %v", err)
	}
}

func TestLoad_VerifyBinary_CommandNotOnPath_SkipsVerification(t *testing.T) {
	pub, _, _ := security.GenerateKeyPair()
	opts := &VerifyOpts{
		TrustedKeys:   []ed25519.PublicKey{pub},
		AllowUnsigned: false,
	}
	mgr := NewManager(slog.Default(), opts, nil)
	plugins := map[string]config.PluginConfig{
		// Command that won't be found on PATH — verifyBinary should skip (not error).
		"missing-binary": {Type: "subprocess", Command: "nonexistent-binary-12345", Capabilities: []string{"tools"}},
	}

	if err := mgr.Load(plugins, nil); err != nil {
		t.Fatalf("Load should succeed when binary not on PATH (deferred check): %v", err)
	}
}

func TestStart_EmptyPluginList_NoError(t *testing.T) {
	mgr := newTestManager()
	mock := &mockToolRegistrar{}
	if err := mgr.Start(context.Background(), mock); err != nil {
		t.Fatalf("expected no error for empty plugin list, got: %v", err)
	}
	if len(mock.registered) != 0 {
		t.Errorf("expected no registrations, got %d", len(mock.registered))
	}
}

func TestStart_MultipleFailures_ReturnsFirstError(t *testing.T) {
	mgr := newTestManager()
	mgr.plugins = []Plugin{
		{Name: "fail-1", Type: TypeSubprocess, Command: "/usr/bin/fail1", Capabilities: []Capability{CapabilityTools}},
		{Name: "fail-2", Type: TypeSubprocess, Command: "/usr/bin/fail2", Capabilities: []Capability{CapabilityTools}},
	}

	mock := &mockToolRegistrar{failNames: map[string]bool{"fail-1": true, "fail-2": true}}
	err := mgr.Start(context.Background(), mock)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "fail-1") {
		t.Errorf("expected first error to reference 'fail-1', got: %v", err)
	}
}

func TestLoad_UnsupportedType_ReturnsError(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"bad-type": {Type: "kubernetes", Command: "whatever", Capabilities: []string{"tools"}},
	}

	err := mgr.Load(plugins, nil)
	if err == nil {
		t.Fatal("expected error for unsupported type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("expected 'unsupported type' in error, got: %v", err)
	}
}
