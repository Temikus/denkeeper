package tool

import (
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// indexProvenanceServer registers sc with tools and rebuilds the derived
// index, so lookups go through the real collision-aware resolution and
// ToolDefs sees the server.
func indexProvenanceServer(m *Manager, sc *serverConn, tools ...string) {
	for _, name := range tools {
		sc.tools = append(sc.tools, llm.ToolDef{Type: "function", Function: llm.FunctionDef{Name: name}})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers[sc.name] = sc
	m.noteDiscoveryOrder(sc.name)
	m.disabledCount += len(sc.disabledSet)
	m.rebuildToolIndex()
}

func externalSSE(name string, cfg config.ToolConfig) *serverConn {
	cfg.Transport = "sse"
	cfg.URL = "http://example.com"
	return &serverConn{name: name, transport: "sse", cfg: cfg, disabledSet: buildDisabledSet(cfg.DisabledTools)}
}

func TestProvenance_InProcessConfigToolIsTrusted(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, &serverConn{name: "config-default"}, "skill_get")

	got := m.Provenance("skill_get")
	if got != (Provenance{Server: "config-default"}) {
		t.Errorf("Provenance(skill_get) = %+v, want server set, not external, not untrusted", got)
	}
}

func TestProvenance_InProcessWebToolsAreUntrusted(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, &serverConn{name: "web-default"}, "web_fetch", "web_search")

	for _, name := range []string{"web_fetch", "web_search"} {
		got := m.Provenance(name)
		if got != (Provenance{Server: "web-default", UntrustedOutput: true}) {
			t.Errorf("Provenance(%s) = %+v, want untrusted, not external", name, got)
		}
	}
}

func TestProvenance_OtherInProcessToolsAreTrusted(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, &serverConn{name: "script-default"}, "run_javascript")
	indexProvenanceServer(m, &serverConn{name: "decide-default"}, "decide")
	indexProvenanceServer(m, &serverConn{name: "config-default"}, "kv_get")

	for _, name := range []string{"run_javascript", "decide", "kv_get"} {
		if got := m.Provenance(name); got.UntrustedOutput || got.External {
			t.Errorf("Provenance(%s) = %+v, want trusted in-process", name, got)
		}
	}
}

func TestProvenance_ExternalServerIsAlwaysUntrusted(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, externalSSE("plain", config.ToolConfig{}), "a")
	indexProvenanceServer(m, externalSSE("readonly", config.ToolConfig{Idempotent: boolPtr(true)}), "b")
	indexProvenanceServer(m, externalSSE("annotated", config.ToolConfig{TrustAnnotations: true}), "c")

	for name, server := range map[string]string{"a": "plain", "b": "readonly", "c": "annotated"} {
		got := m.Provenance(name)
		if got != (Provenance{Server: server, External: true, UntrustedOutput: true}) {
			t.Errorf("Provenance(%s) = %+v, want external and untrusted on %s", name, got, server)
		}
	}
}

func TestProvenance_StdioServerIsExternal(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, &serverConn{name: "browser", command: "npx"}, "browser_navigate")

	got := m.Provenance("browser_navigate")
	if !got.External || !got.UntrustedOutput || got.Server != "browser" {
		t.Errorf("Provenance(browser_navigate) = %+v, want external untrusted on browser", got)
	}
}

func TestProvenance_UnknownAndAmbiguousAreZero(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, externalSSE("alpha", config.ToolConfig{}), "search")
	indexProvenanceServer(m, externalSSE("beta", config.ToolConfig{}), "search")

	if got := m.Provenance("no_such_tool"); got != (Provenance{}) {
		t.Errorf("Provenance(unknown) = %+v, want zero", got)
	}
	if got := m.Provenance("search"); got != (Provenance{}) {
		t.Errorf("Provenance(ambiguous) = %+v, want zero", got)
	}
}

func TestProvenance_QualifiedNameResolvesToItsServer(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, &serverConn{name: "web-default"}, "web_fetch")
	indexProvenanceServer(m, externalSSE("remote", config.ToolConfig{}), "web_fetch")

	if got := m.Provenance("web-default__web_fetch"); got != (Provenance{Server: "web-default", UntrustedOutput: true}) {
		t.Errorf("qualified in-process = %+v", got)
	}
	if got := m.Provenance("remote__web_fetch"); got != (Provenance{Server: "remote", External: true, UntrustedOutput: true}) {
		t.Errorf("qualified external = %+v", got)
	}
}

func TestProvenance_ParentDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	indexProvenanceServer(parent, externalSSE("todoist", config.ToolConfig{}), "find-tasks")
	child := NewManager(testLogger())
	child.AdoptFrom(parent)

	got := child.Provenance("find-tasks")
	if got != (Provenance{Server: "todoist", External: true, UntrustedOutput: true}) {
		t.Errorf("Provenance via parent = %+v", got)
	}
}

func TestAdvertisesUntrustedOutput_ConfigToolsOnly(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, &serverConn{name: "config-default"}, "kv_get", "skill_get")
	if m.AdvertisesUntrustedOutput() {
		t.Error("AdvertisesUntrustedOutput = true with only config tools")
	}
}

func TestAdvertisesUntrustedOutput_ExternalServer(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, &serverConn{name: "config-default"}, "kv_get")
	indexProvenanceServer(m, externalSSE("todoist", config.ToolConfig{}), "find-tasks")
	if !m.AdvertisesUntrustedOutput() {
		t.Error("AdvertisesUntrustedOutput = false with an external server")
	}
}

func TestAdvertisesUntrustedOutput_AllExternalToolsDisabled(t *testing.T) {
	m := NewManager(testLogger())
	indexProvenanceServer(m, &serverConn{name: "config-default"}, "kv_get")
	indexProvenanceServer(m, externalSSE("todoist", config.ToolConfig{DisabledTools: []string{"find-tasks"}}), "find-tasks")
	if m.AdvertisesUntrustedOutput() {
		t.Error("AdvertisesUntrustedOutput = true although the external server's only tool is disabled")
	}
}

func TestAdvertisesUntrustedOutput_ThroughParent(t *testing.T) {
	parent := NewManager(testLogger())
	indexProvenanceServer(parent, &serverConn{name: "web-default"}, "web_fetch")
	child := NewManager(testLogger())
	child.AdoptFrom(parent)
	indexProvenanceServer(child, &serverConn{name: "config-default"}, "kv_get")
	if !child.AdvertisesUntrustedOutput() {
		t.Error("AdvertisesUntrustedOutput = false although the parent advertises web_fetch")
	}
}
