package tool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func newTestLifecycleMgr(t *testing.T) (*LifecycleManager, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte("[telegram]\ntoken = \"test\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(testLogger())
	return NewLifecycleManager(mgr, cfgPath, 5, testLogger()), cfgPath
}

func TestValidateToolName_Empty(t *testing.T) {
	if err := validateToolName(""); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestValidateToolName_Invalid(t *testing.T) {
	if err := validateToolName("has spaces"); err == nil {
		t.Error("expected error for name with spaces")
	}
	if err := validateToolName("../escape"); err == nil {
		t.Error("expected error for name with path traversal")
	}
}

func TestValidateToolName_Valid(t *testing.T) {
	for _, name := range []string{"web-search", "my_tool", "tool123", "A"} {
		if err := validateToolName(name); err != nil {
			t.Errorf("expected no error for %q, got: %v", name, err)
		}
	}
}

func TestLifecycleManager_TrackPlugin(t *testing.T) {
	lm, _ := newTestLifecycleMgr(t)
	lm.TrackPlugin("test-plugin", config.PluginConfig{
		Type:    "subprocess",
		Command: "/usr/bin/test",
	})

	plugins := lm.ListPlugins()
	if len(plugins) != 1 {
		t.Fatalf("ListPlugins() count = %d, want 1", len(plugins))
	}
	if plugins[0].Name != "test-plugin" {
		t.Errorf("plugin name = %q, want test-plugin", plugins[0].Name)
	}
	if plugins[0].Type != "subprocess" {
		t.Errorf("plugin type = %q, want subprocess", plugins[0].Type)
	}
}

func TestLifecycleManager_ListTools_Empty(t *testing.T) {
	lm, _ := newTestLifecycleMgr(t)
	tools := lm.ListTools()
	if len(tools) != 0 {
		t.Errorf("ListTools() on empty manager = %d, want 0", len(tools))
	}
}

func TestLifecycleManager_MaxToolsLimit(t *testing.T) {
	lm, _ := newTestLifecycleMgr(t)
	// Set max to 1 and inject a server to fill the limit.
	lm.maxTools = 1
	lm.toolMgr.servers["existing"] = &serverConn{name: "existing", command: "/bin/test"}

	err := lm.checkLimit()
	if err == nil {
		t.Error("expected error when at max tools limit")
	}
}

func TestLifecycleManager_ConflictDetection(t *testing.T) {
	lm, _ := newTestLifecycleMgr(t)
	lm.toolMgr.servers["taken"] = &serverConn{name: "taken", command: "/bin/taken"}

	err := lm.checkConflict("taken")
	if err == nil {
		t.Error("expected conflict error for existing server name")
	}
}

func TestLifecycleManager_UpdateTool_NotFound(t *testing.T) {
	lm, _ := newTestLifecycleMgr(t)
	cfg := config.ToolConfig{Command: "/usr/bin/new-cmd"}
	err := lm.UpdateTool(t.Context(), "nonexistent", cfg)
	if err == nil {
		t.Error("expected error updating nonexistent tool")
	}
}

func TestLifecycleManager_UpdateTool_MissingCommand(t *testing.T) {
	lm, _ := newTestLifecycleMgr(t)
	// Inject a fake server so existence check passes.
	lm.toolMgr.servers["fake"] = &serverConn{name: "fake", command: "/bin/old", transport: "stdio"}

	cfg := config.ToolConfig{Transport: "stdio"} // no command
	err := lm.UpdateTool(t.Context(), "fake", cfg)
	if err == nil {
		t.Error("expected error when command is missing for stdio transport")
	}
}

func TestLifecycleManager_UpdateTool_MissingURL(t *testing.T) {
	lm, _ := newTestLifecycleMgr(t)
	lm.toolMgr.servers["fake"] = &serverConn{name: "fake", url: "http://old", transport: "sse"}

	cfg := config.ToolConfig{Transport: "sse"} // no url
	err := lm.UpdateTool(t.Context(), "fake", cfg)
	if err == nil {
		t.Error("expected error when url is missing for sse transport")
	}
}

func TestBuildPluginDockerArgs(t *testing.T) {
	cfg := config.PluginConfig{
		Type:        "docker",
		Image:       "ghcr.io/test:v1",
		Network:     "bridge",
		MemoryLimit: "256m",
		CPULimit:    "0.5",
		Env:         map[string]string{"KEY": "val"},
	}

	args := buildPluginDockerArgs(cfg)

	// Verify essential flags are present.
	found := make(map[string]bool)
	for i, a := range args {
		found[a] = true
		if a == "--network" && i+1 < len(args) {
			if args[i+1] != "bridge" {
				t.Errorf("network = %q, want bridge", args[i+1])
			}
		}
		if a == "--memory" && i+1 < len(args) {
			if args[i+1] != "256m" {
				t.Errorf("memory = %q, want 256m", args[i+1])
			}
		}
	}

	if !found["run"] {
		t.Error("missing 'run' arg")
	}
	if !found["--rm"] {
		t.Error("missing '--rm' arg")
	}
	if !found["--cap-drop"] {
		t.Error("missing '--cap-drop' arg")
	}

	// Image should be in args.
	lastArgs := args[len(args)-1]
	// Image should appear somewhere in args list.
	imageFound := false
	for _, a := range args {
		if a == "ghcr.io/test:v1" {
			imageFound = true
		}
	}
	if !imageFound {
		t.Errorf("image not found in docker args, last arg = %q", lastArgs)
	}
}
