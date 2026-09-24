package tool

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func TestIsReadOnly_BuiltinInProcess(t *testing.T) {
	m := NewManager(testLogger())
	// In-process (RegisterSession-style) servers have no transport and no command.
	sc := &serverConn{name: "config-default"}
	for _, name := range []string{"kv_get", "kv_list", "skill_get", "skill_list", "tool_list", "persona_get", "kv_set", "skill_update"} {
		m.toolMap[name] = sc
	}

	for _, name := range []string{"kv_get", "kv_list", "skill_get", "skill_list", "tool_list", "persona_get"} {
		if !m.IsReadOnly(name) {
			t.Errorf("IsReadOnly(%q) = false, want true (built-in read-only allowlist)", name)
		}
	}
	for _, name := range []string{"kv_set", "skill_update"} {
		if m.IsReadOnly(name) {
			t.Errorf("IsReadOnly(%q) = true, want false (a write)", name)
		}
	}
}

// TestIsReadOnly_WiderThanIdempotent pins the deliberate divergence: a config
// read is read-only but must never be memoized, because the same turn can
// write config between two calls.
func TestIsReadOnly_WiderThanIdempotent(t *testing.T) {
	m := NewManager(testLogger())
	m.toolMap["skill_get"] = &serverConn{name: "config-default"}

	if !m.IsReadOnly("skill_get") {
		t.Error("IsReadOnly(skill_get) = false, want true")
	}
	if m.IsIdempotent("skill_get") {
		t.Error("IsIdempotent(skill_get) = true, want false")
	}
}

func TestIsReadOnly_UnknownToolIsNotReadOnly(t *testing.T) {
	m := NewManager(testLogger())
	if m.IsReadOnly("no_such_tool") {
		t.Error("IsReadOnly on unknown tool = true, want false (unclassified is never read-only)")
	}
}

func TestIsReadOnly_ExternalDefaultFalse(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{name: "ext", transport: "sse", cfg: config.ToolConfig{Transport: "sse", URL: "http://example.com"}}
	m.toolMap["issue_get"] = sc
	m.toolMap["kv_get"] = sc

	if m.IsReadOnly("issue_get") {
		t.Error("IsReadOnly(issue_get) = true, want false (external tools default to false)")
	}
	if m.IsReadOnly("kv_get") {
		t.Error("IsReadOnly(kv_get) on an external server = true, want false (the builtin list is in-process only)")
	}
}

func TestIsReadOnly_ExternalOperatorOptIn(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{name: "jira", transport: "sse",
		cfg: config.ToolConfig{Transport: "sse", IdempotentTools: []string{"issue_get"}}}
	m.toolMap["issue_get"] = sc
	m.toolMap["issue_create"] = sc

	if !m.IsReadOnly("issue_get") {
		t.Error("IsReadOnly(issue_get) = false, want true (operator declared it read-only)")
	}
	if m.IsReadOnly("issue_create") {
		t.Error("IsReadOnly(issue_create) = true, want false (not declared)")
	}
}

func TestIsReadOnly_TrustedAnnotation(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{name: "docs", transport: "sse",
		cfg:            config.ToolConfig{Transport: "sse", TrustAnnotations: true},
		readOnlyHinted: map[string]bool{"docs_search": true}}
	m.toolMap["docs_search"] = sc
	m.toolMap["docs_write"] = sc

	if !m.IsReadOnly("docs_search") {
		t.Error("IsReadOnly(docs_search) = false, want true (readOnlyHint + trust_annotations)")
	}
	if m.IsReadOnly("docs_write") {
		t.Error("IsReadOnly(docs_write) = true, want false (no hint)")
	}
}

func TestIsReadOnly_HintIgnoredWithoutTrust(t *testing.T) {
	m := NewManager(testLogger())
	m.toolMap["docs_search"] = &serverConn{name: "docs", transport: "sse",
		cfg:            config.ToolConfig{Transport: "sse"},
		readOnlyHinted: map[string]bool{"docs_search": true}}

	if m.IsReadOnly("docs_search") {
		t.Error("IsReadOnly = true, want false: a server's self-description counts only under trust_annotations")
	}
}

func TestIsReadOnly_AmbiguousNameIsNotReadOnly(t *testing.T) {
	// Two servers advertising one name: the bare name is unroutable, so there
	// is no single tool to classify — even though both sides declared it
	// read-only. Servers start before the manager: cleanups run in reverse, and
	// the MCP sessions must close before the httptest servers they hold open.
	servers := map[string]*httptest.Server{
		"alpha": startCollisionServer(t, "alpha", "search"),
		"beta":  startCollisionServer(t, "beta", "search"),
	}
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	t.Cleanup(func() { _ = m.Close() })
	for name, ts := range servers {
		if err := m.RegisterServer(context.Background(), name, config.ToolConfig{
			Transport: "sse", URL: ts.URL, AllowLoopback: true,
			Idempotent: boolPtr(true),
		}); err != nil {
			t.Fatalf("RegisterServer(%s): %v", name, err)
		}
	}

	if m.IsReadOnly("search") {
		t.Error("IsReadOnly on an ambiguous name = true, want false")
	}
	if !m.IsReadOnly("alpha__search") {
		t.Error("IsReadOnly(alpha__search) = false, want true (the qualified name resolves)")
	}
}

func TestIsReadOnly_ParentDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	parent.toolMap["docs_search"] = &serverConn{name: "docs", transport: "sse",
		cfg: config.ToolConfig{Transport: "sse", Idempotent: boolPtr(true)}}

	child := NewManager(testLogger())
	child.AdoptFrom(parent)

	if !child.IsReadOnly("docs_search") {
		t.Error("IsReadOnly via parent delegation = false, want true")
	}
}
