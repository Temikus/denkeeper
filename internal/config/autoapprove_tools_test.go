package config

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// agents.auto_approve_tools — parsing, validation, persistence
// ---------------------------------------------------------------------------

func TestParse_AgentAutoApproveTools(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]
session_tier = "supervised"
supervisor = "guard"
auto_approve_tools = ["run_javascript", "find-completed-tasks", "get-productivity-stats"]

[[agents]]
name = "guard"
persona_dir = "/agents/guard"
session_tier = "autonomous"
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := cfg.Agents[0].AutoApproveTools
	want := []string{"run_javascript", "find-completed-tasks", "get-productivity-stats"}
	if len(got) != len(want) {
		t.Fatalf("auto_approve_tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("auto_approve_tools = %v, want %v", got, want)
		}
	}
	if len(cfg.Agents[1].AutoApproveTools) != 0 {
		t.Errorf("guard auto_approve_tools = %v, want empty", cfg.Agents[1].AutoApproveTools)
	}
}

// A hyphenated MCP tool name must survive validation — ValidResourceName is
// deliberately not applied to this field.
func TestParse_AgentAutoApproveTools_HyphensAllowed(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]
auto_approve_tools = ["find-completed-tasks"]
`)
	if _, err := Parse(tomlData); err != nil {
		t.Fatalf("hyphenated tool name rejected: %v", err)
	}
}

// The field is inert but legal on a non-supervised agent, so a tier flip does
// not require re-entering the list.
func TestParse_AgentAutoApproveTools_NonSupervisedTierAllowed(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]
session_tier = "autonomous"
auto_approve_tools = ["run_javascript"]
`)
	cfg, err := Parse(tomlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Agents[0].AutoApproveTools) != 1 {
		t.Errorf("auto_approve_tools = %v, want the list to be preserved", cfg.Agents[0].AutoApproveTools)
	}
}

func TestParse_AgentAutoApproveTools_EmptyEntry(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]
auto_approve_tools = ["run_javascript", ""]
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for empty auto_approve_tools entry")
	}
	if !strings.Contains(err.Error(), "auto_approve_tools[1]: tool name must not be empty") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_AgentAutoApproveTools_WhitespaceEntry(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]
auto_approve_tools = ["run javascript"]
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for whitespace in auto_approve_tools entry")
	}
	if !strings.Contains(err.Error(), "must not contain whitespace") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParse_AgentAutoApproveTools_DuplicateEntry(t *testing.T) {
	tomlData := []byte(baseConfig + `
[[agents]]
name = "default"
persona_dir = "/agents/default"
adapters = ["telegram"]
auto_approve_tools = ["run_javascript", "run_javascript"]
`)
	_, err := Parse(tomlData)
	if err == nil {
		t.Fatal("expected error for duplicate auto_approve_tools entry")
	}
	if !strings.Contains(err.Error(), `duplicate tool name "run_javascript"`) {
		t.Errorf("unexpected error: %v", err)
	}
}

// The TOML writer round-trips the whole agent table, so an unrelated field
// update must not drop the auto-approve allowlist.
func TestUpdateAgentInConfig_PreservesAutoApproveTools(t *testing.T) {
	path := writeTestConfig(t, `[api]
enabled = true

[[agents]]
name = "default"
session_tier = "supervised"
auto_approve_tools = ["run_javascript", "find-completed-tasks"]
`)

	if err := UpdateAgentInConfig(path, "default", map[string]any{"llm_model": "new-model"}); err != nil {
		t.Fatalf("UpdateAgentInConfig: %v", err)
	}

	content := readTestConfig(t, path)
	if !strings.Contains(content, "run_javascript") || !strings.Contains(content, "find-completed-tasks") {
		t.Errorf("auto_approve_tools lost on unrelated update; content:\n%s", content)
	}
	if !strings.Contains(content, "new-model") {
		t.Errorf("llm_model not updated; content:\n%s", content)
	}
}
