package tool

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestToolNames_Empty(t *testing.T) {
	m := NewManager(testLogger())
	names := m.ToolNames()
	if len(names) != 0 {
		t.Errorf("ToolNames() on empty manager = %v, want []", names)
	}
}

func TestAdoptFrom_ParentDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	parent.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "parent_tool"}},
	}
	parent.toolMap = map[string]*serverConn{
		"parent_tool": {name: "parent-server"},
	}

	child := NewManager(testLogger())
	child.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "child_tool"}},
	}
	child.AdoptFrom(parent)

	// Child should see both parent and own tools.
	names := child.ToolNames()
	if len(names) != 2 {
		t.Fatalf("ToolNames() count = %d, want 2; got %v", len(names), names)
	}

	defs := child.ToolDefs()
	if len(defs) != 2 {
		t.Fatalf("ToolDefs() count = %d, want 2", len(defs))
	}
	// Parent tools come first.
	if defs[0].Function.Name != "parent_tool" {
		t.Errorf("ToolDefs()[0] = %q, want parent_tool", defs[0].Function.Name)
	}
	if defs[1].Function.Name != "child_tool" {
		t.Errorf("ToolDefs()[1] = %q, want child_tool", defs[1].Function.Name)
	}
}

func TestAdoptFrom_RuntimeToolVisibility(t *testing.T) {
	parent := NewManager(testLogger())
	child := NewManager(testLogger())
	child.AdoptFrom(parent)

	// Initially empty.
	if len(child.ToolDefs()) != 0 {
		t.Fatal("expected no tools initially")
	}

	// Add a tool to parent at "runtime".
	parent.mu.Lock()
	parent.toolDefs = append(parent.toolDefs, llm.ToolDef{
		Type: "function", Function: llm.FunctionDef{Name: "runtime_tool"},
	})
	parent.toolMap["runtime_tool"] = &serverConn{name: "runtime-server"}
	parent.mu.Unlock()

	// Child should see it immediately.
	names := child.ToolNames()
	if len(names) != 1 || names[0] != "runtime_tool" {
		t.Errorf("child.ToolNames() = %v, want [runtime_tool]", names)
	}
}

func TestAdoptFrom_ServerNamesDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	parent.servers["parent-server"] = &serverConn{name: "parent-server"}

	child := NewManager(testLogger())
	child.servers["child-server"] = &serverConn{name: "child-server"}
	child.AdoptFrom(parent)

	names := child.ServerNames()
	if len(names) != 2 {
		t.Fatalf("ServerNames() count = %d, want 2; got %v", len(names), names)
	}

	// Both servers should be found.
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["parent-server"] || !found["child-server"] {
		t.Errorf("ServerNames() = %v, want both parent-server and child-server", names)
	}
}

func TestAdoptFrom_ServerInfoDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	parent.servers["parent-server"] = &serverConn{name: "parent-server", command: "parent-cmd"}

	child := NewManager(testLogger())
	child.AdoptFrom(parent)

	// Child should find parent's server via delegation.
	info, ok := child.ServerInfo("parent-server")
	if !ok {
		t.Fatal("ServerInfo(parent-server) not found via delegation")
	}
	if info.Command != "parent-cmd" {
		t.Errorf("ServerInfo.Command = %q, want parent-cmd", info.Command)
	}

	// Non-existent server should return false.
	_, ok = child.ServerInfo("nonexistent")
	if ok {
		t.Error("ServerInfo(nonexistent) should return false")
	}
}

func TestToolNames_WithTools(t *testing.T) {
	m := NewManager(testLogger())
	m.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "search_web"}},
		{Type: "function", Function: llm.FunctionDef{Name: "read_url"}},
		{Type: "function", Function: llm.FunctionDef{Name: "shell_exec"}},
	}

	names := m.ToolNames()
	if len(names) != 3 {
		t.Fatalf("ToolNames() count = %d, want 3", len(names))
	}
	want := []string{"search_web", "read_url", "shell_exec"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("ToolNames()[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestManager_ServerToolConfig_NotFound(t *testing.T) {
	m := NewManager(testLogger())
	_, ok := m.ServerToolConfig("nonexistent")
	if ok {
		t.Error("expected ServerToolConfig to return false for unknown server")
	}
}

func TestManager_ServerToolConfig_ReturnsStoredConfig(t *testing.T) {
	m := NewManager(testLogger())
	cfg := config.ToolConfig{Command: "/usr/bin/test", Args: []string{"--verbose"}, Transport: "stdio"}
	m.servers["my-tool"] = &serverConn{name: "my-tool", command: cfg.Command, transport: cfg.Transport, cfg: cfg}

	got, ok := m.ServerToolConfig("my-tool")
	if !ok {
		t.Fatal("expected ServerToolConfig to return true for registered server")
	}
	if got.Command != cfg.Command {
		t.Errorf("Command = %q, want %q", got.Command, cfg.Command)
	}
	if len(got.Args) != 1 || got.Args[0] != "--verbose" {
		t.Errorf("Args = %v, want [--verbose]", got.Args)
	}
}

func TestToolDefs_FiltersDisabledTools(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{
		name: "test-server",
		cfg:  config.ToolConfig{Command: "test"},
	}
	m.servers["test-server"] = sc
	m.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "tool-a"}},
		{Type: "function", Function: llm.FunctionDef{Name: "tool-b"}},
		{Type: "function", Function: llm.FunctionDef{Name: "tool-c"}},
	}
	m.toolMap = map[string]*serverConn{
		"tool-a": sc,
		"tool-b": sc,
		"tool-c": sc,
	}

	if err := m.SetDisabledTools("test-server", []string{"tool-b"}); err != nil {
		t.Fatal(err)
	}

	defs := m.ToolDefs()
	if len(defs) != 2 {
		t.Fatalf("ToolDefs() count = %d, want 2; got %v", len(defs), toolNames(defs))
	}
	for _, td := range defs {
		if td.Function.Name == "tool-b" {
			t.Error("disabled tool-b should not appear in ToolDefs()")
		}
	}
}

func TestServerToolDefs_ReturnsAllIncludingDisabled(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{
		name: "test-server",
		cfg:  config.ToolConfig{Command: "test"},
	}
	m.servers["test-server"] = sc
	m.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "tool-a"}},
		{Type: "function", Function: llm.FunctionDef{Name: "tool-b"}},
	}
	m.toolMap = map[string]*serverConn{
		"tool-a": sc,
		"tool-b": sc,
	}
	_ = m.SetDisabledTools("test-server", []string{"tool-b"})

	defs, ok := m.ServerToolDefs("test-server")
	if !ok {
		t.Fatal("ServerToolDefs() returned false")
	}
	if len(defs) != 2 {
		t.Errorf("ServerToolDefs() count = %d, want 2 (disabled tools included)", len(defs))
	}
}

func TestExecute_RejectsDisabledTool(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{
		name:        "test-server",
		disabledSet: map[string]bool{"blocked-tool": true},
	}
	m.servers["test-server"] = sc
	m.toolMap = map[string]*serverConn{"blocked-tool": sc}

	_, err := m.Execute(t.Context(), llm.ToolCall{
		ID:       "call-1",
		Function: llm.FunctionCall{Name: "blocked-tool", Arguments: "{}"},
	})
	if err == nil {
		t.Fatal("Execute() should reject disabled tool")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error = %q, should mention 'disabled'", err.Error())
	}
}

func TestSetDisabledTools(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{
		name: "test-server",
		cfg:  config.ToolConfig{Command: "test"},
	}
	m.servers["test-server"] = sc
	m.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "tool-a"}},
		{Type: "function", Function: llm.FunctionDef{Name: "tool-b"}},
	}
	m.toolMap = map[string]*serverConn{
		"tool-a": sc,
		"tool-b": sc,
	}

	// Disable tool-a.
	if err := m.SetDisabledTools("test-server", []string{"tool-a"}); err != nil {
		t.Fatal(err)
	}

	defs := m.ToolDefs()
	if len(defs) != 1 {
		t.Fatalf("ToolDefs() count = %d, want 1 after disabling", len(defs))
	}
	if defs[0].Function.Name != "tool-b" {
		t.Errorf("remaining tool = %q, want tool-b", defs[0].Function.Name)
	}

	// Verify cfg was updated.
	cfg, _ := m.ServerToolConfig("test-server")
	if len(cfg.DisabledTools) != 1 || cfg.DisabledTools[0] != "tool-a" {
		t.Errorf("cfg.DisabledTools = %v, want [tool-a]", cfg.DisabledTools)
	}

	// Re-enable all.
	if err := m.SetDisabledTools("test-server", nil); err != nil {
		t.Fatal(err)
	}
	if len(m.ToolDefs()) != 2 {
		t.Error("all tools should be enabled after clearing disabled set")
	}
}

func TestSetDisabledTools_ServerNotFound(t *testing.T) {
	m := NewManager(testLogger())
	err := m.SetDisabledTools("nonexistent", []string{"x"})
	if err == nil {
		t.Fatal("expected error for nonexistent server")
	}
}

func TestServerInfo_DisabledToolCounts(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{
		name:        "test-server",
		transport:   "stdio",
		disabledSet: map[string]bool{"tool-b": true},
		cfg:         config.ToolConfig{Command: "test", DisabledTools: []string{"tool-b"}},
	}
	m.servers["test-server"] = sc
	m.toolMap = map[string]*serverConn{
		"tool-a": sc,
		"tool-b": sc,
		"tool-c": sc,
	}

	info, ok := m.ServerInfo("test-server")
	if !ok {
		t.Fatal("ServerInfo() not found")
	}
	if info.TotalToolCount != 3 {
		t.Errorf("TotalToolCount = %d, want 3", info.TotalToolCount)
	}
	if info.EnabledCount != 2 {
		t.Errorf("EnabledCount = %d, want 2", info.EnabledCount)
	}
	if len(info.DisabledTools) != 1 || info.DisabledTools[0] != "tool-b" {
		t.Errorf("DisabledTools = %v, want [tool-b]", info.DisabledTools)
	}
}

func TestSetDisabledTools_NonexistentToolName(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{
		name: "test-server",
		cfg:  config.ToolConfig{Command: "test"},
	}
	m.servers["test-server"] = sc
	m.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "real-tool"}},
	}
	m.toolMap = map[string]*serverConn{"real-tool": sc}

	// Disable a tool name that doesn't exist on the server.
	if err := m.SetDisabledTools("test-server", []string{"nonexistent-tool"}); err != nil {
		t.Fatal(err)
	}

	// The real tool should still be visible since the disabled name doesn't match.
	defs := m.ToolDefs()
	if len(defs) != 1 {
		t.Fatalf("ToolDefs() count = %d, want 1", len(defs))
	}
	if defs[0].Function.Name != "real-tool" {
		t.Errorf("remaining tool = %q, want real-tool", defs[0].Function.Name)
	}

	// ServerInfo should still report the phantom disabled entry.
	info, _ := m.ServerInfo("test-server")
	if info.TotalToolCount != 1 {
		t.Errorf("TotalToolCount = %d, want 1", info.TotalToolCount)
	}
	if info.EnabledCount != 1 {
		t.Errorf("EnabledCount = %d, want 1 (phantom disabled name doesn't count)", info.EnabledCount)
	}
	if len(info.DisabledTools) != 1 || info.DisabledTools[0] != "nonexistent-tool" {
		t.Errorf("DisabledTools = %v, want [nonexistent-tool]", info.DisabledTools)
	}

	// Config round-trip: the nonexistent name should persist.
	cfg, _ := m.ServerToolConfig("test-server")
	if len(cfg.DisabledTools) != 1 || cfg.DisabledTools[0] != "nonexistent-tool" {
		t.Errorf("cfg.DisabledTools = %v, want [nonexistent-tool]", cfg.DisabledTools)
	}
}

func toolNames(defs []llm.ToolDef) []string {
	names := make([]string, len(defs))
	for i, td := range defs {
		names[i] = td.Function.Name
	}
	return names
}
