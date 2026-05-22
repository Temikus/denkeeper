package mcpserver

import (
	"context"
	"testing"
)

func TestHasScope_ExactMatch(t *testing.T) {
	if !hasScope([]string{"chat", "agents:read"}, "chat") {
		t.Error("expected chat scope to match")
	}
}

func TestHasScope_AdminGrantsAll(t *testing.T) {
	if !hasScope([]string{"admin"}, "anything") {
		t.Error("admin should grant any scope")
	}
}

func TestHasScope_Missing(t *testing.T) {
	if hasScope([]string{"agents:read"}, "chat") {
		t.Error("expected no match for missing scope")
	}
}

func TestHasScope_Empty(t *testing.T) {
	if hasScope(nil, "chat") {
		t.Error("nil scopes should not match")
	}
}

func TestRequireScope_Allowed(t *testing.T) {
	ctx := withScopes(context.Background(), []string{"admin"})
	if result := requireScope(ctx, "chat"); result != nil {
		t.Error("expected nil (allowed)")
	}
}

func TestRequireScope_Denied(t *testing.T) {
	ctx := withScopes(context.Background(), []string{"agents:read"})
	result := requireScope(ctx, "chat")
	if result == nil {
		t.Fatal("expected error result")
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = withScopes(ctx, []string{"a", "b"})
	ctx = withKeyName(ctx, "test-key")

	scopes := scopesFromCtx(ctx)
	if len(scopes) != 2 || scopes[0] != "a" {
		t.Errorf("unexpected scopes: %v", scopes)
	}

	name := keyNameFromCtx(ctx)
	if name != "test-key" {
		t.Errorf("unexpected key name: %s", name)
	}
}

func TestContextHelpers_Empty(t *testing.T) {
	ctx := context.Background()
	if scopes := scopesFromCtx(ctx); scopes != nil {
		t.Errorf("expected nil scopes from empty context, got %v", scopes)
	}
	if name := keyNameFromCtx(ctx); name != "" {
		t.Errorf("expected empty key name, got %s", name)
	}
}

func TestToolText(t *testing.T) {
	r := toolText("hello")
	if r.IsError {
		t.Error("expected non-error")
	}
	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content, got %d", len(r.Content))
	}
}

func TestToolError(t *testing.T) {
	r := toolError("bad")
	if !r.IsError {
		t.Error("expected error")
	}
}

func TestToolJSON(t *testing.T) {
	r, err := toolJSON(map[string]int{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError {
		t.Error("expected non-error")
	}
}
