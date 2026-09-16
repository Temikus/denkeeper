package llm

import (
	"context"
	"testing"
)

func filterTestTools() []ToolDef {
	return []ToolDef{
		{Type: "function", Function: FunctionDef{Name: "gmail_list"}},
		{Type: "function", Function: FunctionDef{Name: "kv_get"}},
	}
}

func onlyTool(name string) ToolFilter {
	return func(defs []ToolDef) []ToolDef {
		out := make([]ToolDef, 0, len(defs))
		for _, td := range defs {
			if td.Function.Name == name {
				out = append(out, td)
			}
		}
		return out
	}
}

func TestRouter_CompleteFiltered_NarrowsToolPayload(t *testing.T) {
	r := NewRouter("mock", "model", NewCostTracker(SessionLimits{Hard: 10.0}, nil))
	cap := &toolCapturingProvider{}
	cap.name = "mock"
	cap.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(cap)
	r.SetTools(filterTestTools)

	if _, err := r.CompleteFiltered(context.Background(), "s1",
		[]Message{{Role: "user", Content: "hi"}}, onlyTool("kv_get")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cap.lastTools) != 1 || cap.lastTools[0].Function.Name != "kv_get" {
		t.Errorf("tools = %v, want only kv_get", cap.lastTools)
	}
}

// A nil filter must take exactly the pre-existing branch, so an ungated turn
// puts the same request on the wire as before filtering existed.
func TestRouter_CompleteFiltered_NilFilterAdvertisesEverything(t *testing.T) {
	r := NewRouter("mock", "model", NewCostTracker(SessionLimits{Hard: 10.0}, nil))
	cap := &toolCapturingProvider{}
	cap.name = "mock"
	cap.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(cap)
	r.SetTools(filterTestTools)

	if _, err := r.CompleteFiltered(context.Background(), "s1",
		[]Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	filtered := cap.lastTools

	if _, err := r.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(filtered) != len(cap.lastTools) {
		t.Fatalf("nil-filter tools = %d, plain Complete = %d — the two must be identical",
			len(filtered), len(cap.lastTools))
	}
	for i := range filtered {
		if filtered[i].Function.Name != cap.lastTools[i].Function.Name {
			t.Errorf("tool %d = %q, want %q", i, filtered[i].Function.Name, cap.lastTools[i].Function.Name)
		}
	}
}

// The filter is applied to the payload the fallback retry inherits, so a
// rerouted request cannot re-advertise what the turn hid.
func TestRouter_CompleteFiltered_FallbackRetryStaysFiltered(t *testing.T) {
	r := NewRouter("primary", "model", NewCostTracker(SessionLimits{Hard: 10.0}, nil))
	cap := &toolCapturingProvider{}
	cap.name = "secondary"
	cap.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(&mockProvider{name: "primary", err: &LLMError{StatusCode: 503, Message: "down"}})
	r.RegisterProvider(cap)
	r.SetFallbacks([]FallbackRule{{Trigger: "error", Action: "switch_provider", Provider: "secondary"}})
	r.SetTools(filterTestTools)

	if _, err := r.CompleteFiltered(context.Background(), "s1",
		[]Message{{Role: "user", Content: "hi"}}, onlyTool("kv_get")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cap.lastTools) != 1 || cap.lastTools[0].Function.Name != "kv_get" {
		t.Errorf("fallback tools = %v, want only kv_get", cap.lastTools)
	}
}

func TestRouter_CompleteStreamFiltered_NarrowsToolPayload(t *testing.T) {
	r := NewRouter("mock", "model", NewCostTracker(SessionLimits{Hard: 10.0}, nil))
	cap := &toolCapturingProvider{}
	cap.name = "mock"
	cap.response = &ChatResponse{Content: "ok", TokensUsed: TokenUsage{Total: 5}}
	r.RegisterProvider(cap)
	r.SetTools(filterTestTools)

	if _, err := r.CompleteStreamFiltered(context.Background(), "s1",
		[]Message{{Role: "user", Content: "hi"}}, nil, onlyTool("gmail_list")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cap.lastTools) != 1 || cap.lastTools[0].Function.Name != "gmail_list" {
		t.Errorf("tools = %v, want only gmail_list", cap.lastTools)
	}
}
