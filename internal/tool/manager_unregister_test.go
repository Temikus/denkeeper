package tool

import (
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

func TestUnregisterServer_NotRegistered(t *testing.T) {
	m := NewManager(testLogger())
	err := m.UnregisterServer("no-such-server")
	if err == nil {
		t.Fatal("expected error for unregistered server, got nil")
	}
}

func TestUnregisterServer_RemovesToolDefs(t *testing.T) {
	m := NewManager(testLogger())

	// Manually inject a server and its tools to avoid real subprocess spawning.
	sc := &serverConn{name: "test-srv", command: "/usr/bin/test"}
	m.servers["test-srv"] = sc
	m.toolMap["tool_a"] = sc
	m.toolMap["tool_b"] = sc
	m.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "tool_a"}},
		{Type: "function", Function: llm.FunctionDef{Name: "tool_b"}},
		{Type: "function", Function: llm.FunctionDef{Name: "tool_other"}},
	}
	// tool_other belongs to a different server
	otherSC := &serverConn{name: "other-srv"}
	m.servers["other-srv"] = otherSC
	m.toolMap["tool_other"] = otherSC

	// UnregisterServer will try to close the session, which is nil here.
	// We accept the error from Close but care about the cleanup.
	_ = m.UnregisterServer("test-srv")

	// Verify server is removed.
	if _, ok := m.servers["test-srv"]; ok {
		t.Error("server should have been removed from servers map")
	}

	// Verify tool_a and tool_b are removed from toolMap.
	if _, ok := m.toolMap["tool_a"]; ok {
		t.Error("tool_a should have been removed from toolMap")
	}
	if _, ok := m.toolMap["tool_b"]; ok {
		t.Error("tool_b should have been removed from toolMap")
	}

	// Verify tool_other remains.
	if _, ok := m.toolMap["tool_other"]; !ok {
		t.Error("tool_other should still be in toolMap")
	}

	// Verify toolDefs only contains tool_other.
	if len(m.toolDefs) != 1 {
		t.Fatalf("toolDefs count = %d, want 1", len(m.toolDefs))
	}
	if m.toolDefs[0].Function.Name != "tool_other" {
		t.Errorf("remaining toolDef name = %q, want tool_other", m.toolDefs[0].Function.Name)
	}
}

func TestServerNames_Empty(t *testing.T) {
	m := NewManager(testLogger())
	names := m.ServerNames()
	if len(names) != 0 {
		t.Errorf("ServerNames() on empty manager = %v, want []", names)
	}
}

func TestServerNames_WithServers(t *testing.T) {
	m := NewManager(testLogger())
	m.servers["alpha"] = &serverConn{name: "alpha"}
	m.servers["beta"] = &serverConn{name: "beta"}

	names := m.ServerNames()
	if len(names) != 2 {
		t.Fatalf("ServerNames() count = %d, want 2", len(names))
	}
}

func TestServerInfo_NotFound(t *testing.T) {
	m := NewManager(testLogger())
	_, ok := m.ServerInfo("nope")
	if ok {
		t.Error("expected ok=false for missing server")
	}
}

func TestServerInfo_Found(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{name: "my-tool", command: "/usr/bin/my-tool", args: []string{"--flag"}}
	m.servers["my-tool"] = sc
	m.toolMap["do_thing"] = sc

	info, ok := m.ServerInfo("my-tool")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if info.Name != "my-tool" {
		t.Errorf("Name = %q, want my-tool", info.Name)
	}
	if info.Command != "/usr/bin/my-tool" {
		t.Errorf("Command = %q, want /usr/bin/my-tool", info.Command)
	}
	if len(info.ToolNames) != 1 || info.ToolNames[0] != "do_thing" {
		t.Errorf("ToolNames = %v, want [do_thing]", info.ToolNames)
	}
	if info.Status != "connected" {
		t.Errorf("Status = %q, want connected", info.Status)
	}
}

func TestServerToolDefs_NotFound(t *testing.T) {
	m := NewManager(testLogger())
	_, ok := m.ServerToolDefs("nope")
	if ok {
		t.Error("expected ok=false for missing server")
	}
}

func TestServerToolDefs_ReturnsOnlyServerDefs(t *testing.T) {
	m := NewManager(testLogger())
	sc1 := &serverConn{name: "server-a"}
	sc2 := &serverConn{name: "server-b"}
	m.servers["server-a"] = sc1
	m.servers["server-b"] = sc2
	m.toolMap["tool_a1"] = sc1
	m.toolMap["tool_a2"] = sc1
	m.toolMap["tool_b1"] = sc2
	m.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "tool_a1", Description: "A1"}},
		{Type: "function", Function: llm.FunctionDef{Name: "tool_a2", Description: "A2"}},
		{Type: "function", Function: llm.FunctionDef{Name: "tool_b1", Description: "B1"}},
	}

	defs, ok := m.ServerToolDefs("server-a")
	if !ok {
		t.Fatal("expected ok=true for server-a")
	}
	if len(defs) != 2 {
		t.Fatalf("ServerToolDefs(server-a) returned %d defs, want 2", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Function.Name] = true
	}
	if !names["tool_a1"] || !names["tool_a2"] {
		t.Errorf("unexpected defs: %v", defs)
	}

	defs2, ok2 := m.ServerToolDefs("server-b")
	if !ok2 {
		t.Fatal("expected ok=true for server-b")
	}
	if len(defs2) != 1 || defs2[0].Function.Name != "tool_b1" {
		t.Errorf("ServerToolDefs(server-b) = %v, want [tool_b1]", defs2)
	}
}
