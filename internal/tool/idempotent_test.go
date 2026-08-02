package tool

import (
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestIsIdempotent_BuiltinInProcess(t *testing.T) {
	m := NewManager(testLogger())
	// In-process (RegisterSession-style) servers have no transport and no command.
	sc := &serverConn{name: "web-default"}
	m.toolMap["web_fetch"] = sc
	m.toolMap["web_search"] = sc
	m.toolMap["kv_get"] = sc
	m.toolMap["kv_list"] = sc
	m.toolMap["skill_get"] = sc

	for _, name := range []string{"web_fetch", "web_search", "kv_get", "kv_list"} {
		if !m.IsIdempotent(name) {
			t.Errorf("IsIdempotent(%q) = false, want true (built-in allowlist)", name)
		}
	}
	// In-process tools outside the allowlist are never idempotent.
	if m.IsIdempotent("skill_get") {
		t.Error("IsIdempotent(skill_get) = true, want false (not on the built-in allowlist)")
	}
}

func TestIsIdempotent_UnknownTool(t *testing.T) {
	m := NewManager(testLogger())
	if m.IsIdempotent("no_such_tool") {
		t.Error("IsIdempotent on unknown tool = true, want false")
	}
}

func TestIsIdempotent_ExternalDefaultFalse(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{name: "ext", transport: "sse", cfg: config.ToolConfig{Transport: "sse", URL: "http://example.com"}}
	// Even a name on the built-in allowlist stays false for external servers
	// without an explicit config opt-in.
	m.toolMap["web_fetch"] = sc
	m.toolMap["issue_get"] = sc

	if m.IsIdempotent("issue_get") {
		t.Error("IsIdempotent(issue_get) = true, want false (external tools default to false)")
	}
	if m.IsIdempotent("web_fetch") {
		t.Error("IsIdempotent(web_fetch) on an external server = true, want false (builtin list is in-process only)")
	}
}

func TestIsIdempotent_ExternalServerFlag(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{name: "docs", transport: "sse",
		cfg: config.ToolConfig{Transport: "sse", Idempotent: boolPtr(true)}}
	m.toolMap["docs_search"] = sc

	if !m.IsIdempotent("docs_search") {
		t.Error("IsIdempotent(docs_search) = false, want true (server-wide idempotent flag)")
	}
}

func TestIsIdempotent_ExternalPerToolList(t *testing.T) {
	m := NewManager(testLogger())
	sc := &serverConn{name: "jira", transport: "sse",
		cfg: config.ToolConfig{Transport: "sse", IdempotentTools: []string{"issue_get", "issue_search"}}}
	m.toolMap["issue_get"] = sc
	m.toolMap["issue_create"] = sc

	if !m.IsIdempotent("issue_get") {
		t.Error("IsIdempotent(issue_get) = false, want true (listed in idempotent_tools)")
	}
	if m.IsIdempotent("issue_create") {
		t.Error("IsIdempotent(issue_create) = true, want false (not listed)")
	}
}

func TestIsIdempotent_ParentDelegation(t *testing.T) {
	parent := NewManager(testLogger())
	parent.toolMap["docs_search"] = &serverConn{name: "docs", transport: "sse",
		cfg: config.ToolConfig{Transport: "sse", Idempotent: boolPtr(true)}}

	child := NewManager(testLogger())
	child.AdoptFrom(parent)

	if !child.IsIdempotent("docs_search") {
		t.Error("IsIdempotent via parent delegation = false, want true")
	}
}

func TestIsIdempotentTool_ConfigHelper(t *testing.T) {
	var zero config.ToolConfig
	if zero.IsIdempotentTool("anything") {
		t.Error("zero-value ToolConfig should not be idempotent")
	}
	off := config.ToolConfig{Idempotent: boolPtr(false), IdempotentTools: []string{"a"}}
	if !off.IsIdempotentTool("a") || off.IsIdempotentTool("b") {
		t.Error("idempotent=false must not disable the per-tool list; unlisted tools stay false")
	}
}

func TestToolConfigToMap_IdempotentFields(t *testing.T) {
	// Omitted when unset/false.
	entry := toolConfigToMap(config.ToolConfig{Command: "x", Idempotent: boolPtr(false)})
	if _, ok := entry["idempotent"]; ok {
		t.Error("idempotent=false must be omitted from TOML")
	}
	if _, ok := entry["idempotent_tools"]; ok {
		t.Error("empty idempotent_tools must be omitted from TOML")
	}

	entry = toolConfigToMap(config.ToolConfig{
		Command: "x", Idempotent: boolPtr(true), IdempotentTools: []string{"lookup"},
	})
	if v, ok := entry["idempotent"].(bool); !ok || !v {
		t.Errorf("idempotent = %v, want true", entry["idempotent"])
	}
	tools, ok := entry["idempotent_tools"].([]string)
	if !ok || len(tools) != 1 || tools[0] != "lookup" {
		t.Errorf("idempotent_tools = %v, want [lookup]", entry["idempotent_tools"])
	}
}
