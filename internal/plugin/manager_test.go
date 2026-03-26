package plugin

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

// mockToolRegistrar records RegisterServer calls and can simulate failures.
type mockToolRegistrar struct {
	registered []string
	failNames  map[string]bool
}

func (m *mockToolRegistrar) RegisterServer(_ context.Context, name, _ string, _ []string, _ map[string]string) error {
	if m.failNames[name] {
		return errors.New("mock: simulated failure")
	}
	m.registered = append(m.registered, name)
	return nil
}

func newTestManager() *Manager {
	return NewManager(slog.Default())
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

func TestLoad_DockerType_ReturnsNotImplementedError(t *testing.T) {
	mgr := newTestManager()
	plugins := map[string]config.PluginConfig{
		"docker-plugin": {Type: "docker", Command: "some-image", Capabilities: []string{"tools"}},
	}

	err := mgr.Load(plugins, nil)
	if err == nil {
		t.Fatal("expected error for docker type, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "not yet implemented") {
		t.Errorf("expected error to contain 'not yet implemented', got: %s", got)
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
	if len(mock.registered) != 1 || mock.registered[0] != "plugin-ok" {
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

