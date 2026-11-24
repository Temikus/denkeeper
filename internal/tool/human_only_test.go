package tool

import "testing"

// In-process servers (RegisterSession-style) have no transport and no
// command; an external one has a transport.
func inProcessConn(name string) *serverConn { return &serverConn{name: name} }

func externalConn(name string) *serverConn {
	return &serverConn{name: name, transport: "sse"}
}

func TestRequiresHumanApproval_InProcessBuiltin(t *testing.T) {
	m := NewManager(testLogger())
	seedServerTools(m, inProcessConn("config-default"), toolDef("tool_add"), toolDef("plugin_add"), toolDef("kv_get"))

	for _, name := range []string{"tool_add", "plugin_add"} {
		if !m.RequiresHumanApproval(name) {
			t.Errorf("RequiresHumanApproval(%q) = false, want true", name)
		}
	}
	if m.RequiresHumanApproval("kv_get") {
		t.Error("RequiresHumanApproval(kv_get) = true, want false")
	}
}

// An external server's tool that happens to be called tool_add is not the
// in-process installer.
func TestRequiresHumanApproval_ExternalSameNameNotGated(t *testing.T) {
	m := NewManager(testLogger())
	seedServerTools(m, externalConn("ext"), toolDef("tool_add"))

	if m.RequiresHumanApproval("tool_add") {
		t.Error("RequiresHumanApproval(tool_add) on an external server = true, want false")
	}
}

// When an external server collides with the in-process tool_add, the bare
// name is ambiguous. It must still answer true, or an agent could dodge the
// gate by registering a colliding server.
func TestRequiresHumanApproval_AmbiguousNameGated(t *testing.T) {
	m := NewManager(testLogger())
	seedServerTools(m, inProcessConn("config-default"), toolDef("tool_add"))
	seedServerTools(m, externalConn("ext"), toolDef("tool_add"))

	if !m.RequiresHumanApproval("tool_add") {
		t.Error("RequiresHumanApproval on the ambiguous bare name = false, want true")
	}
}

// Under a collision each owner is advertised by its qualified name, and the
// answer follows the owner that name routes to.
func TestRequiresHumanApproval_CollisionQualifiedNames(t *testing.T) {
	m := NewManager(testLogger())
	seedServerTools(m, inProcessConn("config-default"), toolDef("tool_add"))
	seedServerTools(m, externalConn("ext"), toolDef("tool_add"))

	if !m.RequiresHumanApproval("config-default__tool_add") {
		t.Error("RequiresHumanApproval(config-default__tool_add) = false, want true")
	}
	if m.RequiresHumanApproval("ext__tool_add") {
		t.Error("RequiresHumanApproval(ext__tool_add) = true, want false (external owner)")
	}
}

func TestRequiresHumanApproval_ParentDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	seedServerTools(parent, inProcessConn("config-default"), toolDef("plugin_add"))

	child := NewManager(testLogger())
	child.AdoptFrom(parent)

	if !child.RequiresHumanApproval("plugin_add") {
		t.Error("RequiresHumanApproval via parent delegation = false, want true")
	}
	if child.RequiresHumanApproval("no_such_tool") {
		t.Error("RequiresHumanApproval(no_such_tool) = true, want false")
	}
}
