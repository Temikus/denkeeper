package approval

import (
	"context"
	"testing"
)

func TestShouldAutoApprove_NoRules(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	ok, _ := m.ShouldAutoApprove(ctx, "default", "web_search", "conv1")
	if ok {
		t.Error("expected no auto-approve with no rules")
	}
}

func TestShouldAutoApprove_SessionRule(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.AddSessionRule("default", "web_search", "conv1", "test")

	ok, scope := m.ShouldAutoApprove(ctx, "default", "web_search", "conv1")
	if !ok {
		t.Fatal("expected auto-approve to match session rule")
	}
	if scope != ScopeSession {
		t.Errorf("expected scope %q, got %q", ScopeSession, scope)
	}
}

func TestShouldAutoApprove_SessionRuleDifferentConversation(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.AddSessionRule("default", "web_search", "conv1", "test")

	ok, _ := m.ShouldAutoApprove(ctx, "default", "web_search", "conv2")
	if ok {
		t.Error("session rule should not match different conversation")
	}
}

func TestShouldAutoApprove_PermanentRule(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	_, err := m.AddPermanentRule(ctx, "default", "web_search", "test")
	if err != nil {
		t.Fatalf("AddPermanentRule: %v", err)
	}

	ok, scope := m.ShouldAutoApprove(ctx, "default", "web_search", "any-conv")
	if !ok {
		t.Fatal("expected auto-approve to match permanent rule")
	}
	if scope != ScopePermanent {
		t.Errorf("expected scope %q, got %q", ScopePermanent, scope)
	}
}

func TestShouldAutoApprove_SessionTakesPrecedence(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.AddSessionRule("default", "web_search", "conv1", "test")
	if _, err := m.AddPermanentRule(ctx, "default", "web_search", "test"); err != nil {
		t.Fatal(err)
	}

	// Session should be checked first.
	ok, scope := m.ShouldAutoApprove(ctx, "default", "web_search", "conv1")
	if !ok {
		t.Fatal("expected match")
	}
	if scope != ScopeSession {
		t.Errorf("expected session scope (takes precedence), got %q", scope)
	}
}

func TestAddPermanentRule_Persistence(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	rule, err := m.AddPermanentRule(ctx, "agent1", "tool_a", "api")
	if err != nil {
		t.Fatal(err)
	}
	if rule.ID == "" {
		t.Error("expected non-empty rule ID")
	}
	if rule.Scope != ScopePermanent {
		t.Errorf("expected permanent scope, got %q", rule.Scope)
	}

	// Should be listed.
	rules, err := m.ListAutoApproveRules(ctx, "agent1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rules {
		if r.ID == rule.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("permanent rule not found in listing")
	}
}

func TestRemoveAutoApproveRule(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	rule, err := m.AddPermanentRule(ctx, "default", "web_search", "test")
	if err != nil {
		t.Fatal(err)
	}

	if err := m.RemoveAutoApproveRule(ctx, rule.ID); err != nil {
		t.Fatalf("RemoveAutoApproveRule: %v", err)
	}

	ok, _ := m.ShouldAutoApprove(ctx, "default", "web_search", "conv1")
	if ok {
		t.Error("rule should no longer match after removal")
	}
}

func TestRemoveAutoApproveRule_NotFound(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	err := m.RemoveAutoApproveRule(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent rule")
	}
}

func TestClearSessionRules(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.AddSessionRule("default", "tool_a", "conv1", "test")
	m.AddSessionRule("default", "tool_b", "conv1", "test")
	m.AddSessionRule("default", "tool_a", "conv2", "test")

	m.ClearSessionRules("conv1")

	ok, _ := m.ShouldAutoApprove(ctx, "default", "tool_a", "conv1")
	if ok {
		t.Error("conv1 tool_a should be cleared")
	}
	ok, _ = m.ShouldAutoApprove(ctx, "default", "tool_b", "conv1")
	if ok {
		t.Error("conv1 tool_b should be cleared")
	}
	// conv2 should be unaffected.
	ok, _ = m.ShouldAutoApprove(ctx, "default", "tool_a", "conv2")
	if !ok {
		t.Error("conv2 rule should still be active")
	}
}

func TestListAutoApproveRules_CombinesSessionAndPermanent(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	m.AddSessionRule("default", "tool_a", "conv1", "test")
	if _, err := m.AddPermanentRule(ctx, "default", "tool_b", "test"); err != nil {
		t.Fatal(err)
	}

	rules, err := m.ListAutoApproveRules(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(rules))
	}
}

func TestExtractToolName(t *testing.T) {
	tests := []struct {
		summary  string
		expected string
	}{
		{`Execute tool "web_search" with args: {"q":"test"}`, "web_search"},
		{`Execute tool "send_email" with args: {}`, "send_email"},
		{`Something else`, ""},
		{``, ""},
	}
	for _, tt := range tests {
		got := ExtractToolName(tt.summary)
		if got != tt.expected {
			t.Errorf("ExtractToolName(%q) = %q, want %q", tt.summary, got, tt.expected)
		}
	}
}

func TestParseCallback(t *testing.T) {
	tests := []struct {
		data     string
		prefix   string
		action   CallbackAction
		ok       bool
	}{
		{"appr:abc123:approve", "appr:abc123", CallbackApprove, true},
		{"appr:abc123:deny", "appr:abc123", CallbackDeny, true},
		{"appr:abc123:approve_session", "appr:abc123", CallbackApproveSession, true},
		{"appr:abc123:approve_always", "appr:abc123", CallbackApproveAlways, true},
		{"other:data", "", "", false},
		{"appr:abc123:unknown", "", "", false},
	}
	for _, tt := range tests {
		prefix, action, ok := parseCallback(tt.data)
		if ok != tt.ok {
			t.Errorf("parseCallback(%q): ok=%v, want %v", tt.data, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if prefix != tt.prefix {
			t.Errorf("parseCallback(%q): prefix=%q, want %q", tt.data, prefix, tt.prefix)
		}
		if action != tt.action {
			t.Errorf("parseCallback(%q): action=%q, want %q", tt.data, action, tt.action)
		}
	}
}

func TestResolveByCallback_ApproveSession_CreatesSessionRule(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	// Submit a tool call approval.
	noOp := func(_ context.Context, _ string) error { return nil }
	req, err := m.Submit(ctx, "default", ActionKindToolCall,
		`Execute tool "web_search" with args: {"q":"test"}`, `{"q":"test"}`,
		"123", "telegram", "conv1", noOp)
	if err != nil {
		t.Fatal(err)
	}

	// Resolve with approve_session.
	_, err = m.ResolveByCallback(ctx, req.CallbackData+":approve_session", "telegram")
	if err != nil {
		t.Fatalf("ResolveByCallback: %v", err)
	}

	// Session rule should now exist.
	ok, scope := m.ShouldAutoApprove(ctx, "default", "web_search", "conv1")
	if !ok {
		t.Error("expected session auto-approve rule to be created")
	}
	if scope != ScopeSession {
		t.Errorf("expected session scope, got %q", scope)
	}
}

func TestResolveByCallback_ApproveAlways_CreatesPermanentRule(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	noOp := func(_ context.Context, _ string) error { return nil }
	req, err := m.Submit(ctx, "default", ActionKindToolCall,
		`Execute tool "send_email" with args: {}`, `{}`,
		"123", "telegram", "conv1", noOp)
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.ResolveByCallback(ctx, req.CallbackData+":approve_always", "telegram")
	if err != nil {
		t.Fatalf("ResolveByCallback: %v", err)
	}

	// Permanent rule should exist (matches any conversation).
	ok, scope := m.ShouldAutoApprove(ctx, "default", "send_email", "any-conv")
	if !ok {
		t.Error("expected permanent auto-approve rule to be created")
	}
	if scope != ScopePermanent {
		t.Errorf("expected permanent scope, got %q", scope)
	}
}
