// Step-boundary stop for panic/cancel — stage 1 of
// design/plans/6-step-boundary-stop.md. These tests pin the two boundaries (top
// of a tool round, and the gap before each call within a round) and the
// graceful exit; the hard-kill fallback they deliberately do not touch is
// covered by engine_interrupted_test.go.
package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

// hookedSequentialProvider replays canned responses and calls beforeCall (with
// the 1-based call number) before each one, so a test can request a stop at an
// exact point in the turn.
type hookedSequentialProvider struct {
	responses  []*llm.ChatResponse
	requests   []llm.ChatRequest
	beforeCall func(call int)
}

func (p *hookedSequentialProvider) Name() string { return "mock" }
func (p *hookedSequentialProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.requests = append(p.requests, req)
	if p.beforeCall != nil {
		p.beforeCall(len(p.requests))
	}
	if len(p.requests) > len(p.responses) {
		return nil, fmt.Errorf("no more mock responses (call %d)", len(p.requests))
	}
	return p.responses[len(p.requests)-1], nil
}
func (p *hookedSequentialProvider) HealthCheck(_ context.Context) error { return nil }

type stopToolArgs struct {
	Query string `json:"query"`
}

// newStopTestEngine builds an autonomous engine whose tool manager hosts two
// counting tools: "step_a" (which additionally runs onStepA, used to raise a
// stop from inside a running call) and "step_b". The counter tracks real
// handler invocations, so a call that was never started leaves it untouched.
func newStopTestEngine(t *testing.T, provider llm.Provider, onStepA func()) (*Engine, *SQLiteMemoryStore, *atomic.Int64) {
	t.Helper()

	var execCount atomic.Int64
	server := mcp.NewServer(&mcp.Implementation{Name: "stop-server", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "step_a", Description: "step a"},
		func(_ context.Context, _ *mcp.CallToolRequest, args stopToolArgs) (*mcp.CallToolResult, any, error) {
			execCount.Add(1)
			if onStepA != nil {
				onStepA()
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "a: " + args.Query}},
			}, nil, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "step_b", Description: "step b"},
		func(_ context.Context, _ *mcp.CallToolRequest, args stopToolArgs) (*mcp.CallToolResult, any, error) {
			execCount.Add(1)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "b: " + args.Query}},
			}, nil, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	toolMgr := tool.NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	if err := toolMgr.RegisterServer(context.Background(), "stop-tool", config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}); err != nil {
		t.Fatalf("RegisterServer: %v", err)
	}
	t.Cleanup(func() { _ = toolMgr.Close() })

	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)
	router.SetTools(func() []llm.ToolDef {
		return []llm.ToolDef{
			{Type: "function", Function: llm.FunctionDef{Name: "step_a"}},
			{Type: "function", Function: llm.FunctionDef{Name: "step_b"}},
		}
	})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	e := NewEngine("default", router, store, nil, permissions, nil, "Test.", nil, toolMgr, nil, testLogger())
	return e, store, &execCount
}

func stopTestMessage(sessionID string) adapter.IncomingMessage {
	return adapter.IncomingMessage{
		Adapter:        "telegram",
		ExternalID:     sessionID,
		ConversationID: sessionID,
		UserID:         "user-1",
		Text:           "Do the multi-step thing",
		Timestamp:      time.Now(),
	}
}

func toolRoundResponse(content string, calls ...llm.ToolCall) *llm.ChatResponse {
	return &llm.ChatResponse{
		Content:      content,
		ToolCalls:    calls,
		TokensUsed:   llm.TokenUsage{Total: 10},
		Model:        "test-model",
		FinishReason: "tool_calls",
	}
}

func stopToolCall(id, name string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: `{"query":"x"}`}}
}

// A stop raised after the first round completes must end the turn at the next
// round boundary: round 2 never starts (no LLM call, no tool execution), the
// turn persists through the normal path with the early-end marker, and round
// 1's records keep their real outcomes.
func TestRunToolLoop_StopFlagExitsAtRoundBoundary(t *testing.T) {
	var engine *Engine
	provider := &hookedSequentialProvider{
		responses: []*llm.ChatResponse{
			toolRoundResponse("Working on it.", stopToolCall("c1", "step_a")),
			toolRoundResponse("", stopToolCall("c2", "step_b")),
			toolRoundResponse("", stopToolCall("c3", "step_b")),
		},
	}
	// Request the stop while the round-1 follow-up completion is being served,
	// i.e. after round 1's tool call has already committed.
	provider.beforeCall = func(call int) {
		if call == 2 {
			engine.RequestStop()
		}
	}

	engine, store, execCount := newStopTestEngine(t, provider, nil)

	sessionID := "stop-round-boundary"
	result, err := engine.ChatWithEvents(context.Background(), stopTestMessage(sessionID), nil)
	if err != nil {
		t.Fatalf("stopped turn should end gracefully, got error: %v", err)
	}
	if !strings.Contains(result, "[engine: turn ended early — stop requested]") {
		t.Errorf("result = %q, want the early-end marker", result)
	}
	if !strings.Contains(result, "Working on it.") {
		t.Errorf("result = %q, want the accumulated intermediate content", result)
	}

	// Initial completion + round-1 follow-up. Round 2's completion must never
	// be issued: the third canned response stays unused.
	if len(provider.requests) != 2 {
		t.Fatalf("got %d provider requests, want 2 (initial + round 1 follow-up)", len(provider.requests))
	}
	if got := execCount.Load(); got != 1 {
		t.Errorf("executed %d tool calls, want 1 (round 2 must not start)", got)
	}

	time.Sleep(100 * time.Millisecond)

	assistants := assistantMessages(t, store, sessionID)
	if len(assistants) != 1 {
		t.Fatalf("got %d assistant messages, want 1", len(assistants))
	}
	if strings.Contains(assistants[0].Content, "[Interrupted after") {
		t.Errorf("content = %q, want the graceful stop marker, not the interrupted-progress marker", assistants[0].Content)
	}
	if !strings.Contains(assistants[0].Content, "turn ended early — stop requested") {
		t.Errorf("stored content = %q, want the early-end marker", assistants[0].Content)
	}

	records, err := store.GetToolCalls(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("getting tool calls: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d tool call records, want 1 (round 1 only)", len(records))
	}
	if records[0].ToolName != "step_a" || records[0].Outcome != "ok" || !records[0].Success {
		t.Errorf("record = %+v, want step_a with its real ok outcome", records[0])
	}
}

// A stop raised while a call is executing must not abandon it: the running call
// finishes and keeps its real outcome, while the calls after it in the same
// round are never started — and never recorded as failures.
func TestRunToolLoop_StopMidRoundFinishesCurrentCall(t *testing.T) {
	var engine *Engine
	provider := &hookedSequentialProvider{
		responses: []*llm.ChatResponse{
			toolRoundResponse("Fanning out.", stopToolCall("c1", "step_a"), stopToolCall("c2", "step_b")),
			{Content: "All done.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	// The stop lands while call 1 of the round is running.
	engine, store, execCount := newStopTestEngine(t, provider, func() { engine.RequestStop() })

	sessionID := "stop-mid-round"
	result, err := engine.ChatWithEvents(context.Background(), stopTestMessage(sessionID), nil)
	if err != nil {
		t.Fatalf("stopped turn should end gracefully, got error: %v", err)
	}
	if !strings.Contains(result, "[engine: turn ended early — stop requested]") {
		t.Errorf("result = %q, want the early-end marker", result)
	}

	if got := execCount.Load(); got != 1 {
		t.Errorf("executed %d tool calls, want 1 (call 2 must not start)", got)
	}
	if len(provider.requests) != 1 {
		t.Errorf("got %d provider requests, want 1 (no follow-up completion after the stop)", len(provider.requests))
	}

	time.Sleep(100 * time.Millisecond)

	records, err := store.GetToolCalls(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("getting tool calls: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d tool call records, want 1 (only the call that ran)", len(records))
	}
	if records[0].ToolName != "step_a" || records[0].Outcome != "ok" {
		t.Errorf("record = %+v, want step_a with its real ok outcome", records[0])
	}
	for _, r := range records {
		if r.ToolName == "step_b" {
			t.Errorf("step_b was recorded (outcome %q) — a call that never started must not appear in telemetry", r.Outcome)
		}
	}
}

// The stop exit must not spend tokens: no wrap-up completion is issued, and the
// turn answers with the content the model had already produced.
func TestRunToolLoop_StopSkipsWrapUpCompletion(t *testing.T) {
	var engine *Engine
	provider := &hookedSequentialProvider{
		responses: []*llm.ChatResponse{
			toolRoundResponse("Partial findings.", stopToolCall("c1", "step_a")),
			toolRoundResponse("", stopToolCall("c2", "step_b")),
		},
	}
	provider.beforeCall = func(call int) {
		if call == 2 {
			engine.RequestStop()
		}
	}
	engine, _, _ = newStopTestEngine(t, provider, nil)

	result, err := engine.ChatWithEvents(context.Background(), stopTestMessage("stop-no-wrapup"), nil)
	if err != nil {
		t.Fatalf("stopped turn should end gracefully, got error: %v", err)
	}
	// A wrap-up would be a third request; the provider has no third response
	// and would error, so the successful return already proves it was skipped.
	if len(provider.requests) != 2 {
		t.Errorf("got %d provider requests, want 2 (no wrap-up completion)", len(provider.requests))
	}
	if !strings.HasPrefix(result, "Partial findings.") {
		t.Errorf("result = %q, want it to start with the accumulated content", result)
	}
}

// A turn that starts after the stop captures the new generation and is
// unaffected — nothing has to be reset for the engine to work again.
func TestRequestStop_DoesNotAffectLaterTurns(t *testing.T) {
	provider := &hookedSequentialProvider{
		responses: []*llm.ChatResponse{
			toolRoundResponse("", stopToolCall("c1", "step_a")),
			{Content: "Finished.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	engine, _, execCount := newStopTestEngine(t, provider, nil)

	engine.RequestStop()

	result, err := engine.ChatWithEvents(context.Background(), stopTestMessage("stop-later-turn"), nil)
	if err != nil {
		t.Fatalf("a turn started after the stop must run normally, got error: %v", err)
	}
	if result != "Finished." {
		t.Errorf("result = %q, want the normal completion", result)
	}
	if got := execCount.Load(); got != 1 {
		t.Errorf("executed %d tool calls, want 1", got)
	}
}

// Dispatcher panic raises the stop on every registered engine, so an in-flight
// turn on any transport ends at its next step boundary through the persistence
// path — while new turns keep being refused at the dispatcher as before.
func TestPanic_SetsStopOnEngines(t *testing.T) {
	var dispatcher *Dispatcher
	provider := &hookedSequentialProvider{
		responses: []*llm.ChatResponse{
			toolRoundResponse("Started.", stopToolCall("c1", "step_a")),
			toolRoundResponse("", stopToolCall("c2", "step_b")),
		},
	}
	provider.beforeCall = func(call int) {
		if call == 2 {
			dispatcher.Panic()
		}
	}
	engine, store, execCount := newStopTestEngine(t, provider, nil)
	dispatcher = NewDispatcher(map[string]*Engine{"default": engine}, nil, nil, testLogger())

	before := engine.StopGeneration()

	sessionID := "panic-stop-session"
	// The scheduler path: Dispatch never registers in inFlight, so the context
	// sweep cannot reach this turn — only the cooperative stop can.
	if err := dispatcher.Dispatch(context.Background(), "default", stopTestMessage(sessionID)); err != nil {
		t.Fatalf("stopped turn should end gracefully, got error: %v", err)
	}

	if after := engine.StopGeneration(); after == before {
		t.Errorf("stop generation = %d, want it bumped from %d by the panic", after, before)
	}
	if !dispatcher.IsPanicked() {
		t.Error("dispatcher should be panicked")
	}
	if got := execCount.Load(); got != 1 {
		t.Errorf("executed %d tool calls, want 1 (the round after the panic must not start)", got)
	}

	time.Sleep(100 * time.Millisecond)

	assistants := assistantMessages(t, store, sessionID)
	if len(assistants) != 1 {
		t.Fatalf("got %d assistant messages, want 1 (the stop marker, not a dangling user message)", len(assistants))
	}
	if !strings.Contains(assistants[0].Content, "turn ended early — stop requested") {
		t.Errorf("stored content = %q, want the early-end marker", assistants[0].Content)
	}
	records, err := store.GetToolCalls(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("getting tool calls: %v", err)
	}
	if len(records) != 1 || records[0].Outcome != "ok" {
		t.Errorf("records = %+v, want the single executed call with its real outcome", records)
	}
}
