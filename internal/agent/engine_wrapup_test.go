package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

// capturingSequentialProvider replays canned responses like sequentialProvider
// but also records every ChatRequest it receives, so tests can assert on the
// request shape (e.g. that the wrap-up completion carries no tool definitions).
type capturingSequentialProvider struct {
	responses []*llm.ChatResponse
	requests  []llm.ChatRequest
}

func (p *capturingSequentialProvider) Name() string { return "mock" }
func (p *capturingSequentialProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if len(p.requests) > len(p.responses) {
		return nil, fmt.Errorf("no more mock responses (call %d)", len(p.requests))
	}
	return p.responses[len(p.requests)-1], nil
}
func (p *capturingSequentialProvider) HealthCheck(_ context.Context) error { return nil }

func newWrapUpTestEngine(t *testing.T, provider llm.Provider, router *llm.Router) (*Engine, *SQLiteMemoryStore) {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "Test.", nil, toolMgr, nil, testLogger())
	return engine, store
}

func wrapUpTestMessage(sessionID string) adapter.IncomingMessage {
	return adapter.IncomingMessage{
		Adapter:        "test",
		ExternalID:     sessionID,
		ConversationID: sessionID,
		UserID:         "user-1",
		Text:           "Do the thing",
		Timestamp:      time.Now(),
	}
}

func repeatCallResponses(extra ...*llm.ChatResponse) []*llm.ChatResponse {
	sameCall := llm.ToolCall{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "find_tasks", Arguments: `{"filter":"overdue"}`}}
	responses := []*llm.ChatResponse{
		{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
		{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
		{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
	}
	return append(responses, extra...)
}

// The wrap-up completion must carry no tool definitions: the "no more tools"
// contract is enforced by request shape, so the provider cannot return
// further tool calls no matter what the model wants.
func TestWrapUp_RequestOmitsTools(t *testing.T) {
	provider := &capturingSequentialProvider{
		responses: repeatCallResponses(
			&llm.ChatResponse{Content: "Summary of findings.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		),
	}
	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)
	router.SetTools(func() []llm.ToolDef {
		return []llm.ToolDef{{Type: "function", Function: llm.FunctionDef{Name: "find_tasks"}}}
	})
	engine, _ := newWrapUpTestEngine(t, provider, router)

	result, err := engine.ChatWithEvents(context.Background(), wrapUpTestMessage("wrapup-omit-tools"), nil)
	if err != nil {
		t.Fatalf("expected wrap-up success, got error: %v", err)
	}
	if !strings.Contains(result, "Summary of findings.") {
		t.Errorf("result = %q, want wrap-up summary text", result)
	}

	if len(provider.requests) != 4 {
		t.Fatalf("got %d provider requests, want 4 (initial + 2 round completions + wrap-up)", len(provider.requests))
	}
	for i, req := range provider.requests[:3] {
		if len(req.Tools) == 0 {
			t.Errorf("request %d: tools missing, want tool definitions on normal rounds", i)
		}
	}
	wrapReq := provider.requests[3]
	if len(wrapReq.Tools) != 0 {
		t.Errorf("wrap-up request carries %d tool definitions, want 0", len(wrapReq.Tools))
	}

	// The wrap-up request must end with the engine's wrap-up instruction, and
	// the suppressed third call must have a synthetic tool result so the
	// message protocol stays valid.
	last := wrapReq.Messages[len(wrapReq.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "tool loop stopped (repeated identical tool calls)") {
		t.Errorf("last message = [%s] %q, want the wrap-up instruction", last.Role, last.Content)
	}
	var syntheticSeen bool
	for _, m := range wrapReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "[engine: call not executed") {
			syntheticSeen = true
		}
	}
	if !syntheticSeen {
		t.Error("no synthetic tool result for the suppressed call in wrap-up request")
	}
}

// When the wrap-up completion itself fails, behavior degrades to exactly the
// pre-wrap-up path: the turn errors and persistInterruptedProgress stores the
// interruption marker plus the executed tool records.
func TestWrapUp_CompletionFails_FallsBackToMarker(t *testing.T) {
	// Only the 3 tool_calls responses — the 4th (wrap-up) call errors.
	provider := &capturingSequentialProvider{responses: repeatCallResponses()}
	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)
	engine, store := newWrapUpTestEngine(t, provider, router)

	sessionID := "wrapup-fail-session"
	_, err := engine.ChatWithEvents(context.Background(), wrapUpTestMessage(sessionID), nil)
	if err == nil {
		t.Fatal("expected error when wrap-up completion fails")
	}
	if !strings.Contains(err.Error(), "wrap-up failed") {
		t.Errorf("error = %q, want it to mention the failed wrap-up", err.Error())
	}

	time.Sleep(100 * time.Millisecond)

	assistants := assistantMessages(t, store, sessionID)
	if len(assistants) != 1 {
		t.Fatalf("got %d assistant messages, want 1", len(assistants))
	}
	if !strings.Contains(assistants[0].Content, "[Interrupted after 2 tool call(s)") {
		t.Errorf("content = %q, want the interruption marker", assistants[0].Content)
	}

	records, err := store.GetToolCalls(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("getting tool calls: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("got %d tool call records, want 2 (executed before the stop)", len(records))
	}
}

// A whitespace-only wrap-up response falls back to accumulated intermediate
// content without issuing a second nudge completion.
func TestWrapUp_WhitespaceContent_UsesAccumulated(t *testing.T) {
	sameCall := llm.ToolCall{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "find_tasks", Arguments: `{"filter":"overdue"}`}}
	provider := &capturingSequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "Gathered 3 tasks so far.", ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{Content: "   ", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)
	engine, _ := newWrapUpTestEngine(t, provider, router)

	result, err := engine.ChatWithEvents(context.Background(), wrapUpTestMessage("wrapup-whitespace"), nil)
	if err != nil {
		t.Fatalf("expected accumulated-content fallback, got error: %v", err)
	}
	if !strings.Contains(result, "Gathered 3 tasks so far.") {
		t.Errorf("result = %q, want accumulated intermediate content", result)
	}
	if !strings.Contains(result, "[engine: turn ended early — repeated identical tool calls]") {
		t.Errorf("result = %q, want the early-end marker", result)
	}
	// Exactly 4 provider calls: no second nudge after the whitespace wrap-up.
	if len(provider.requests) != 4 {
		t.Errorf("got %d provider requests, want 4 (no extra nudge)", len(provider.requests))
	}
}

// The defensive no-records branch: a loop stop with zero executed tool calls
// has nothing to summarize, so it must take the plain-error path WITHOUT
// issuing a wrap-up completion. Unreachable through the public path today
// (a repeat trip at threshold 3 executes ≥2 calls first; a max-rounds stop
// requires ≥1 completed round), so it is exercised directly — it becomes
// live if the repeat threshold ever becomes configurable below 3.
func TestFinishStoppedToolLoop_NoRecords_PlainError(t *testing.T) {
	// Provider with no responses: any LLM call would return an error, so a
	// zero-request assertion proves the wrap-up completion was never issued.
	provider := &capturingSequentialProvider{}
	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)
	engine, _ := newWrapUpTestEngine(t, provider, router)

	msgs := []llm.Message{{Role: "user", Content: "Do the thing"}}
	out := toolLoopOutcome{
		llmMessages: msgs,
		stopReason:  stopRepeatedCalls,
	}
	var totalUsage llm.TokenUsage
	var totalCost float64

	resp, gotMsgs, err := engine.finishStoppedToolLoop(context.Background(), "conv-norecords", out, &totalUsage, &totalCost, nil)
	if err == nil {
		t.Fatal("expected plain error for a stop with no completed tool calls")
	}
	if !strings.Contains(err.Error(), "no completed tool calls") {
		t.Errorf("error = %q, want it to mention no completed tool calls", err.Error())
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil (no wrap-up response)", resp)
	}
	if len(gotMsgs) != len(msgs) {
		t.Errorf("got %d messages, want %d (no wrap-up instruction appended)", len(gotMsgs), len(msgs))
	}
	if len(provider.requests) != 0 {
		t.Errorf("provider received %d requests, want 0 (wrap-up must not be attempted)", len(provider.requests))
	}
}

func TestToolBudgetHint_ZeroRemaining(t *testing.T) {
	hint := toolBudgetHint(5, 5)
	if !strings.Contains(hint, "0 of 5") {
		t.Errorf("hint = %q, want '0 of 5'", hint)
	}
	if !strings.Contains(hint, "final answer") {
		t.Errorf("hint = %q, want the explicit final-answer instruction", hint)
	}
	// Positive case unchanged.
	if got := toolBudgetHint(5, 3); !strings.Contains(got, "2 of 5 tool-call rounds remaining this turn") {
		t.Errorf("hint = %q, want the standard remaining-rounds phrasing", got)
	}
}
