package approval

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// Config-scoped auto-approve rules (TOML-declared, read-only at runtime)
// ---------------------------------------------------------------------------

func TestShouldAutoApprove_ConfigRule(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.SetConfigRules(ctx, map[string][]string{"default": {"run_javascript"}})

	ok, scope := m.ShouldAutoApprove(ctx, "default", "run_javascript", "conv1")
	if !ok {
		t.Fatal("expected auto-approve to match config rule")
	}
	if scope != ScopeConfig {
		t.Errorf("scope = %q, want %q", scope, ScopeConfig)
	}

	// A tool that is not listed does not match.
	if ok, scope := m.ShouldAutoApprove(ctx, "default", "send_email", "conv1"); ok {
		t.Errorf("unlisted tool matched with scope %q", scope)
	}

	// Rules are agent-scoped: another agent does not inherit them.
	if ok, scope := m.ShouldAutoApprove(ctx, "other", "run_javascript", "conv1"); ok {
		t.Errorf("unlisted agent matched with scope %q", scope)
	}
}

func TestShouldAutoApprove_ConfigRuleIgnoresConversation(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.SetConfigRules(ctx, map[string][]string{"default": {"web_search"}})

	// Config rules are agent-scoped, so any conversation matches.
	if ok, _ := m.ShouldAutoApprove(ctx, "default", "web_search", "some-other-conv"); !ok {
		t.Error("config rule should match regardless of conversation")
	}
}

func TestShouldAutoApprove_Precedence_ConfigBeatsSessionAndPermanent(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.SetConfigRules(ctx, map[string][]string{"default": {"web_search"}})
	m.AddSessionRule(ctx, "default", "web_search", "conv1", "test")
	if _, err := m.AddPermanentRule(ctx, "default", "web_search", "test"); err != nil {
		t.Fatal(err)
	}

	// All three scopes answer yes; attribution must be the stable one.
	ok, scope := m.ShouldAutoApprove(ctx, "default", "web_search", "conv1")
	if !ok {
		t.Fatal("expected match")
	}
	if scope != ScopeConfig {
		t.Errorf("scope = %q, want %q (config is checked first)", scope, ScopeConfig)
	}
}

// TestShouldAutoApprove_ConfigNotWeakenedAtRuntime pins the security property:
// the runtime rule-management surfaces write to different state, so nothing
// short of a config reload can revoke a TOML-declared rule.
func TestShouldAutoApprove_ConfigNotWeakenedAtRuntime(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.SetConfigRules(ctx, map[string][]string{"default": {"web_search"}})
	m.AddSessionRule(ctx, "default", "web_search", "conv1", "test")
	permanent, err := m.AddPermanentRule(ctx, "default", "web_search", "test")
	if err != nil {
		t.Fatal(err)
	}

	// Delete every DB rule and clear the conversation's session rules.
	if err := m.RemoveAutoApproveRule(ctx, permanent.ID); err != nil {
		t.Fatalf("RemoveAutoApproveRule: %v", err)
	}
	m.ClearSessionRules("conv1")

	ok, scope := m.ShouldAutoApprove(ctx, "default", "web_search", "conv1")
	if !ok {
		t.Fatal("config rule must survive removal of session and permanent rules")
	}
	if scope != ScopeConfig {
		t.Errorf("scope = %q, want %q", scope, ScopeConfig)
	}
}

func TestSetConfigRules_ReplaceRemovesStale(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.SetConfigRules(ctx, map[string][]string{"default": {"tool_a", "tool_b"}})
	// Reload narrows the list.
	m.SetConfigRules(ctx, map[string][]string{"default": {"tool_a"}})

	if ok, _ := m.ShouldAutoApprove(ctx, "default", "tool_a", "conv1"); !ok {
		t.Error("tool_a should still match after the narrowing reload")
	}
	if ok, _ := m.ShouldAutoApprove(ctx, "default", "tool_b", "conv1"); ok {
		t.Error("tool_b should no longer match after the narrowing reload")
	}

	// An empty map drops everything.
	m.SetConfigRules(ctx, nil)
	if ok, _ := m.ShouldAutoApprove(ctx, "default", "tool_a", "conv1"); ok {
		t.Error("no config rules should match after an empty reload")
	}
}

func TestSetConfigRules_AutoResolvesPending(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	blessed := false
	if _, err := m.Submit(ctx, "default", ActionKindToolCall,
		`Execute tool "web_search" with args: {"q":"test"}`, `{"q":"test"}`,
		"ext", "ws", "conv1", func(_ context.Context, _ string) error {
			blessed = true
			return nil
		}); err != nil {
		t.Fatal(err)
	}

	// A pending approval for a tool that is NOT blessed must stay pending.
	untouched := false
	if _, err := m.Submit(ctx, "default", ActionKindToolCall,
		`Execute tool "other_tool" with args: {}`, `{}`,
		"ext", "ws", "conv1", func(_ context.Context, _ string) error {
			untouched = true
			return nil
		}); err != nil {
		t.Fatal(err)
	}

	m.SetConfigRules(ctx, map[string][]string{"default": {"web_search"}})

	if !blessed {
		t.Error("pending approval for the newly-blessed tool should be auto-resolved")
	}
	if untouched {
		t.Error("pending approval for an unrelated tool should NOT be auto-resolved")
	}

	pending, err := m.List(ctx, StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Errorf("pending approvals = %d, want 1 (only other_tool)", len(pending))
	}
}

// TestSetConfigRules_AutoResolvesOnlyNewPairs pins that a reload which does not
// change a rule does not re-resolve approvals queued for it — only pairs newly
// present relative to the previous map are auto-resolved.
func TestSetConfigRules_AutoResolvesOnlyNewPairs(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.SetConfigRules(ctx, map[string][]string{"default": {"web_search"}})

	resolved := false
	if _, err := m.Submit(ctx, "default", ActionKindToolCall,
		`Execute tool "web_search" with args: {}`, `{}`,
		"ext", "ws", "conv1", func(_ context.Context, _ string) error {
			resolved = true
			return nil
		}); err != nil {
		t.Fatal(err)
	}

	// Same map again — web_search is not newly present.
	m.SetConfigRules(ctx, map[string][]string{"default": {"web_search"}})

	if resolved {
		t.Error("an unchanged rule should not re-trigger auto-resolution")
	}
}

func TestListAutoApproveRules_IncludesConfig(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.SetConfigRules(ctx, map[string][]string{
		"default": {"zeta_tool", "alpha_tool"},
		"other":   {"other_tool"},
	})

	rules, err := m.ListAutoApproveRules(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules = %d, want 2 (agent filter must exclude 'other')", len(rules))
	}

	// Source is a map — the listing must be sorted by tool name.
	if rules[0].ToolName != "alpha_tool" || rules[1].ToolName != "zeta_tool" {
		t.Errorf("tool order = [%q %q], want [alpha_tool zeta_tool]", rules[0].ToolName, rules[1].ToolName)
	}
	for _, r := range rules {
		if r.Scope != ScopeConfig {
			t.Errorf("scope = %q, want %q", r.Scope, ScopeConfig)
		}
		if r.ID != "" {
			t.Errorf("ID = %q, want empty (config rules cannot be addressed for deletion)", r.ID)
		}
		if r.CreatedBy != "config" {
			t.Errorf("CreatedBy = %q, want config", r.CreatedBy)
		}
		if r.ExpiresAt != nil {
			t.Errorf("ExpiresAt = %v, want nil (config rules never expire)", r.ExpiresAt)
		}
	}

	// No filter returns both agents, sorted by agent then tool.
	all, err := m.ListAutoApproveRules(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered rules = %d, want 3", len(all))
	}
	want := []string{"default/alpha_tool", "default/zeta_tool", "other/other_tool"}
	for i, r := range all {
		if got := r.AgentName + "/" + r.ToolName; got != want[i] {
			t.Errorf("rules[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func TestConfigRuleTools_Sorted(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.SetConfigRules(ctx, map[string][]string{"default": {"zeta", "alpha", "mid"}})

	got := m.ConfigRuleTools("default")
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("ConfigRuleTools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ConfigRuleTools = %v, want %v", got, want)
		}
	}

	if tools := m.ConfigRuleTools("unknown"); tools != nil {
		t.Errorf("ConfigRuleTools(unknown) = %v, want nil", tools)
	}
}
