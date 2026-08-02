package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

type cacheToolArgs struct {
	Query string `json:"query"`
}

// newCacheTestEngine builds an engine whose tool manager hosts a counting
// "lookup" tool (succeeds) and a counting "bad_lookup" tool (returns IsError),
// registered via RegisterServer with the given ToolConfig (transport/URL are
// filled in). The returned counter tracks lookup+bad_lookup handler
// invocations, i.e. real executions.
func newCacheTestEngine(t *testing.T, cfg config.ToolConfig, tier string) (*Engine, *atomic.Int64) {
	t.Helper()

	var execCount atomic.Int64
	server := mcp.NewServer(&mcp.Implementation{Name: "cache-server", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "lookup", Description: "lookup"},
		func(_ context.Context, _ *mcp.CallToolRequest, args cacheToolArgs) (*mcp.CallToolResult, any, error) {
			execCount.Add(1)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "result for " + args.Query}},
			}, nil, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "bad_lookup", Description: "always rejects"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ cacheToolArgs) (*mcp.CallToolResult, any, error) {
			execCount.Add(1)
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "bad args"}},
			}, nil, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	cfg.Transport = "sse"
	cfg.URL = ts.URL
	cfg.AllowLoopback = true
	toolMgr := tool.NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	if err := toolMgr.RegisterServer(context.Background(), "cache-tool", cfg); err != nil {
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
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "done", FinishReason: "stop"}})

	permissions, err := security.NewPermissionEngine(tier)
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	e := NewEngine("default", router, store, nil, permissions, nil, "test", nil, toolMgr, nil, testLogger())
	return e, &execCount
}

func idempotentTrue() *bool { b := true; return &b }

func TestExecuteToolCallDeduped_CachedHit_SkipsExecution(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{Idempotent: idempotentTrue()}, "autonomous")
	tc := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}}
	state := newTurnToolState()

	first, firstRecord := e.executeToolCallDeduped(context.Background(), tc, 1, "conv:1", false, nil, state)
	if firstRecord.Outcome != "ok" {
		t.Fatalf("first Outcome = %q, want ok", firstRecord.Outcome)
	}
	second, record := e.executeToolCallDeduped(context.Background(), tc, 2, "conv:1", false, nil, state)

	if got := execCount.Load(); got != 1 {
		t.Errorf("handler executed %d times, want 1", got)
	}
	if !strings.HasPrefix(second, "[engine: identical call — cached result from round 1]") {
		t.Errorf("cached result = %q, want the cache disclosure prefix", second)
	}
	if !strings.Contains(second, first) {
		t.Errorf("cached result %q does not contain the original result %q", second, first)
	}
	if !record.Success || record.Outcome != "cached" || record.DurationMs != 0 {
		t.Errorf("record = {Success:%v Outcome:%q DurationMs:%d}, want {true cached 0}",
			record.Success, record.Outcome, record.DurationMs)
	}
	if record.ServerName != "cache-tool" {
		t.Errorf("ServerName = %q, want cache-tool", record.ServerName)
	}
}

func TestExecuteToolCallDeduped_NonIdempotent_ReExecutes(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{}, "autonomous")
	tc := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}}
	state := newTurnToolState()

	_, r1 := e.executeToolCallDeduped(context.Background(), tc, 1, "conv:1", false, nil, state)
	_, r2 := e.executeToolCallDeduped(context.Background(), tc, 2, "conv:1", false, nil, state)

	if got := execCount.Load(); got != 2 {
		t.Errorf("handler executed %d times, want 2 (no caching without the idempotent flag)", got)
	}
	if r1.Outcome != "ok" || r2.Outcome != "ok" {
		t.Errorf("outcomes = %q, %q, want ok, ok", r1.Outcome, r2.Outcome)
	}
}

func TestExecuteToolCallDeduped_FailedResult_NotCached(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{Idempotent: idempotentTrue()}, "autonomous")
	tc := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "bad_lookup", Arguments: `{"query":"x"}`}}
	state := newTurnToolState()

	_, r1 := e.executeToolCallDeduped(context.Background(), tc, 1, "conv:1", false, nil, state)
	_, r2 := e.executeToolCallDeduped(context.Background(), tc, 2, "conv:1", false, nil, state)

	if r1.Outcome != "rejected" || r2.Outcome != "rejected" {
		t.Errorf("outcomes = %q, %q, want rejected, rejected", r1.Outcome, r2.Outcome)
	}
	if got := execCount.Load(); got != 2 {
		t.Errorf("handler executed %d times, want 2 (non-ok outcomes are never cached)", got)
	}
}

func TestExecuteToolCallDeduped_CacheKey_ArgSensitive(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{Idempotent: idempotentTrue()}, "autonomous")
	state := newTurnToolState()

	tcA := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"a"}`}}
	tcB := llm.ToolCall{ID: "c2", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"b"}`}}
	_, _ = e.executeToolCallDeduped(context.Background(), tcA, 1, "conv:1", false, nil, state)
	_, record := e.executeToolCallDeduped(context.Background(), tcB, 2, "conv:1", false, nil, state)

	if got := execCount.Load(); got != 2 {
		t.Errorf("handler executed %d times, want 2 (different args must not hit the cache)", got)
	}
	if record.Outcome != "ok" {
		t.Errorf("second Outcome = %q, want ok", record.Outcome)
	}
}

// TestExecuteToolCallDeduped_CachedHit_SkipsApproval pins that a cache hit is
// served before the supervised approval chain: with an approval manager wired
// and no auto-approve rules, a fresh execution would block/deny — a cache hit
// returns immediately with no approval submission and no tool_approval event.
func TestExecuteToolCallDeduped_CachedHit_SkipsApproval(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{Idempotent: idempotentTrue()}, "supervised")
	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	t.Cleanup(func() { _ = approvalStore.Close() })
	mgr := approval.NewManager(approvalStore, testLogger())
	e.approvals = mgr

	tc := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}}
	state := newTurnToolState()
	// Simulate a call that already executed (and passed approval) this turn.
	state.cache[toolDedupeKey(tc)] = cachedToolResult{result: "result for x", round: 1}

	var events []ChatEvent
	result, record := e.executeToolCallDeduped(context.Background(), tc, 2, "conv:1", true,
		func(ev ChatEvent) { events = append(events, ev) }, state)

	if record.Outcome != "cached" {
		t.Fatalf("Outcome = %q, want cached (a miss would have entered the approval chain)", record.Outcome)
	}
	if got := execCount.Load(); got != 0 {
		t.Errorf("handler executed %d times, want 0", got)
	}
	if !strings.Contains(result, "cached result from round 1") {
		t.Errorf("result = %q, want cache disclosure", result)
	}
	pending, err := mgr.List(context.Background(), approval.StatusPending)
	if err != nil {
		t.Fatalf("listing approvals: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending approvals = %d, want 0 (cache hits never enter the approval chain)", len(pending))
	}
	for _, ev := range events {
		if ev.Type == "tool_approval" {
			t.Errorf("unexpected tool_approval event on cache hit: %+v", ev)
		}
	}
}

func TestExecuteToolCallDeduped_DeniedThenNeverCached(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{Idempotent: idempotentTrue()}, "autonomous")
	tc := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}}
	state := newTurnToolState()
	state.denied[toolDedupeKey(tc)] = "Tool call was denied by the operator."

	_, record := e.executeToolCallDeduped(context.Background(), tc, 2, "conv:1", false, nil, state)
	if record.Outcome != "denied" {
		t.Errorf("Outcome = %q, want denied (denial dedup wins; the cache never sees denied calls)", record.Outcome)
	}
	if got := execCount.Load(); got != 0 {
		t.Errorf("handler executed %d times, want 0", got)
	}
	if len(state.cache) != 0 {
		t.Errorf("cache has %d entries, want 0", len(state.cache))
	}
}

func TestCachedToolCallResult_EventsAndAudit(t *testing.T) {
	e, _ := newCacheTestEngine(t, config.ToolConfig{Idempotent: idempotentTrue()}, "autonomous")
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	tc := llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}}
	state := newTurnToolState()
	var events []ChatEvent
	capture := func(ev ChatEvent) { events = append(events, ev) }

	_, _ = e.executeToolCallDeduped(context.Background(), tc, 1, "conv:1", false, capture, state)
	events = nil
	_, _ = e.executeToolCallDeduped(context.Background(), tc, 2, "conv:1", false, capture, state)

	if len(events) != 2 || events[0].Type != "tool_start" || events[1].Type != "tool_end" {
		t.Fatalf("events = %+v, want [tool_start tool_end]", events)
	}
	if events[1].Duration != 0 || !strings.Contains(events[1].Text, "cached result from round 1") {
		t.Errorf("tool_end = %+v, want Duration 0 and cache disclosure text", events[1])
	}
	if events[0].ToolID != "c1" || events[1].ToolID != "c1" || events[0].Round != 2 {
		t.Errorf("event identity fields wrong: %+v", events)
	}

	var hit *audit.Event
	for i := range auditor.events {
		if auditor.events[i].Category == audit.CategoryToolCall && auditor.events[i].Action == "cache_hit" {
			hit = &auditor.events[i]
		}
	}
	if hit == nil {
		t.Fatal("no cache_hit audit event emitted")
	}
	if hit.Status != audit.StatusOK || hit.DurationMs != 0 {
		t.Errorf("cache_hit event = {Status:%v DurationMs:%d}, want {ok 0}", hit.Status, hit.DurationMs)
	}
	if !strings.Contains(hit.Detail, `"cached_from_round":1`) {
		t.Errorf("detail = %q, want cached_from_round", hit.Detail)
	}
	if strings.Contains(hit.Detail, `"result"`) {
		t.Errorf("detail = %q, must not duplicate the result body (round-1 execute event stores it)", hit.Detail)
	}
}

// TestRunToolLoop_CachedHitsStillTripRepeatDetector pins the invariant that the
// idempotent-result cache sits downstream of the repeat detector: a model that
// issues the same call 3 consecutive times still trips stopRepeatedCalls and
// gets the wrap-up round, while the tool itself executed exactly once (repeat
// #2 was served from cache, repeat #3 was stopped before dedup).
func TestRunToolLoop_CachedHitsStillTripRepeatDetector(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{Idempotent: idempotentTrue()}, "autonomous")

	sameCall := llm.ToolCall{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}}
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{Content: "Wrapped.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)
	e.router = router

	result, err := e.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-cache-repeat",
		UserID:     "user-1",
		Text:       "look it up",
		Timestamp:  time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("expected wrap-up success after repeat detection, got error: %v", err)
	}
	if !strings.Contains(result, "[engine: turn ended early — repeated identical tool calls]") {
		t.Errorf("result = %q, want the repeated-calls early-end marker", result)
	}
	if got := execCount.Load(); got != 1 {
		t.Errorf("handler executed %d times, want 1 (repeat #2 cached, repeat #3 stopped)", got)
	}
}

// TestRunToolLoop_DuplicateIdempotentCall_CachedRecordPersisted covers the
// loop-level happy path: the model repeats an identical idempotent call in two
// consecutive rounds (below the repeat threshold), the second is served from
// cache, and both records persist with their respective outcomes.
func TestRunToolLoop_DuplicateIdempotentCall_CachedRecordPersisted(t *testing.T) {
	e, execCount := newCacheTestEngine(t, config.ToolConfig{Idempotent: idempotentTrue()}, "autonomous")

	call := func(id string) llm.ToolCall {
		return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"query":"x"}`}}
	}
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{call("c1")}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{call("c2")}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{Content: "Final.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}
	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)
	e.router = router

	result, err := e.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:        "test",
		ExternalID:     "chat-cache-dup",
		ConversationID: "conv:cache-dup",
		UserID:         "user-1",
		Text:           "look it up twice",
		Timestamp:      time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}
	if result != "Final." {
		t.Errorf("result = %q, want Final.", result)
	}
	if got := execCount.Load(); got != 1 {
		t.Errorf("handler executed %d times, want 1", got)
	}

	store, ok := e.memory.(*SQLiteMemoryStore)
	if !ok {
		t.Fatalf("memory store is %T, want *SQLiteMemoryStore", e.memory)
	}
	records, err := store.GetToolCalls(context.Background(), "conv:cache-dup")
	if err != nil {
		t.Fatalf("GetToolCalls: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("persisted %d tool-call records, want 2", len(records))
	}
	outcomes := map[string]bool{}
	for _, r := range records {
		outcomes[r.Outcome] = true
	}
	if !outcomes["ok"] || !outcomes["cached"] {
		t.Errorf("persisted outcomes = %v, want both ok and cached", outcomes)
	}
}
