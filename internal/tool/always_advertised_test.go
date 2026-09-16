package tool

import (
	"testing"
)

func TestIsAlwaysAdvertised_MarkedServer(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{name: "config-default"}
	m.toolMap["skill_get"] = sc
	m.toolMap["kv_set"] = sc
	m.MarkAlwaysAdvertised("config-default")

	for _, name := range []string{"skill_get", "kv_set"} {
		if !m.IsAlwaysAdvertised(name) {
			t.Errorf("IsAlwaysAdvertised(%q) = false, want true (its server is marked)", name)
		}
	}
}

func TestIsAlwaysAdvertised_UnmarkedServer(t *testing.T) {
	m := NewManager(testLogger())
	m.toolMap["web_fetch"] = &serverConn{name: "web-default"}
	m.MarkAlwaysAdvertised("config-default")

	if m.IsAlwaysAdvertised("web_fetch") {
		t.Error("IsAlwaysAdvertised(web_fetch) = true, want false — task capability is filterable")
	}
	if m.IsAlwaysAdvertised("no_such_tool") {
		t.Error("IsAlwaysAdvertised on an unknown tool = true, want false")
	}
}

func TestIsAlwaysAdvertised_ParentDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	parent.toolMap["skill_get"] = &serverConn{name: "config-shared"}
	parent.MarkAlwaysAdvertised("config-shared")

	child := NewManager(testLogger())
	child.AdoptFrom(parent)

	if !child.IsAlwaysAdvertised("skill_get") {
		t.Error("IsAlwaysAdvertised(skill_get) = false on the child, want true (delegated to the parent)")
	}
}

// A marked server keeps its exemption under a name collision, where the tool is
// advertised as "<server>__<tool>" and the bare name is unroutable.
func TestIsAlwaysAdvertised_QualifiedName(t *testing.T) {
	m := NewManager(testLogger())
	cfgConn := &serverConn{name: "config-default"}
	extConn := &serverConn{name: "ext"}
	m.toolMap["config-default__skill_get"] = cfgConn
	m.toolMap["ext__skill_get"] = extConn
	m.localOf["config-default__skill_get"] = "skill_get"
	m.localOf["ext__skill_get"] = "skill_get"
	m.owners["skill_get"] = []*serverConn{cfgConn, extConn}
	m.MarkAlwaysAdvertised("config-default")

	if !m.IsAlwaysAdvertised("config-default__skill_get") {
		t.Error("qualified name of a marked server = false, want true")
	}
	if m.IsAlwaysAdvertised("ext__skill_get") {
		t.Error("qualified name of an unmarked server = true, want false")
	}
	if m.IsAlwaysAdvertised("skill_get") {
		t.Error("ambiguous bare name = true, want false — it is not advertised at all")
	}
}
