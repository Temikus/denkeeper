package tool

import (
	"io"
	"log/slog"
	"testing"

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
