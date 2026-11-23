package tool

import (
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// addIndexedServer registers sc with the given tools and rebuilds the derived
// tool index, so lookups go through the real collision-aware resolution.
func addIndexedServer(m *Manager, sc *serverConn, tools ...string) {
	for _, name := range tools {
		sc.tools = append(sc.tools, llm.ToolDef{Function: llm.FunctionDef{Name: name}})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[sc.name] = sc
	m.noteDiscoveryOrder(sc.name)
	m.rebuildToolIndex()
}

func TestBuiltinLocalName_InProcessTool(t *testing.T) {
	m := NewManager(testLogger())
	addIndexedServer(m, &serverConn{name: "config-default"}, "kv_set", "kv_get")

	local, ok := m.BuiltinLocalName("kv_set")
	if !ok || local != "kv_set" {
		t.Errorf("BuiltinLocalName(kv_set) = (%q, %v), want (\"kv_set\", true)", local, ok)
	}
}

func TestBuiltinLocalName_ExternalToolIsNotBuiltin(t *testing.T) {
	m := NewManager(testLogger())
	addIndexedServer(m, &serverConn{name: "ext", transport: "sse",
		cfg: config.ToolConfig{Transport: "sse", URL: "http://example.com"}}, "kv_set")

	if local, ok := m.BuiltinLocalName("kv_set"); ok {
		t.Errorf("BuiltinLocalName(kv_set) on an external server = (%q, true), want false", local)
	}
}

func TestBuiltinLocalName_QualifiedInProcessName(t *testing.T) {
	m := NewManager(testLogger())
	addIndexedServer(m, &serverConn{name: "config-default"}, "kv_set")
	addIndexedServer(m, &serverConn{name: "ext", transport: "sse",
		cfg: config.ToolConfig{Transport: "sse", URL: "http://example.com"}}, "kv_set")

	local, ok := m.BuiltinLocalName("config-default__kv_set")
	if !ok || local != "kv_set" {
		t.Errorf("BuiltinLocalName(config-default__kv_set) = (%q, %v), want (\"kv_set\", true)", local, ok)
	}
	if _, ok := m.BuiltinLocalName("ext__kv_set"); ok {
		t.Error("BuiltinLocalName(ext__kv_set) = true, want false (external server)")
	}
}

func TestBuiltinLocalName_AmbiguousNameIsNotBuiltin(t *testing.T) {
	m := NewManager(testLogger())
	addIndexedServer(m, &serverConn{name: "config-default"}, "kv_set")
	addIndexedServer(m, &serverConn{name: "config-other"}, "kv_set")

	if local, ok := m.BuiltinLocalName("kv_set"); ok {
		t.Errorf("BuiltinLocalName on an ambiguous name = (%q, true), want false", local)
	}
}

func TestBuiltinLocalName_ParentDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	addIndexedServer(parent, &serverConn{name: "config-default"}, "skill_update")

	child := NewManager(testLogger())
	child.AdoptFrom(parent)

	local, ok := child.BuiltinLocalName("skill_update")
	if !ok || local != "skill_update" {
		t.Errorf("BuiltinLocalName via parent = (%q, %v), want (\"skill_update\", true)", local, ok)
	}
}

func TestBuiltinLocalName_AmbiguousNameNotAnsweredByParent(t *testing.T) {
	parent := NewManager(testLogger())
	addIndexedServer(parent, &serverConn{name: "config-default"}, "kv_set")

	child := NewManager(testLogger())
	child.AdoptFrom(parent)
	addIndexedServer(child, &serverConn{name: "a"}, "kv_set")
	addIndexedServer(child, &serverConn{name: "b"}, "kv_set")

	if _, ok := child.BuiltinLocalName("kv_set"); ok {
		t.Error("BuiltinLocalName = true, want false: the collision is in the child, so the parent must not answer")
	}
}
