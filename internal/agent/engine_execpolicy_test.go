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
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

type policyToolArgs struct {
	Value string `json:"value"`
}

// newPolicyTestEngine builds an engine whose tool server hosts one tool
// declared idempotent ("read_thing") and one that is not ("write_thing"), so a
// single turn can exercise both sides of the suppression split. The counters
// report real handler invocations.
func newPolicyTestEngine(t *testing.T, responses []*llm.ChatResponse, tier string) (*Engine, *SQLiteMemoryStore, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	return newPolicyTestEngineWithProvider(t, &sequentialProvider{responses: responses}, tier)
}

// newPolicyTestEngineWithProvider is newPolicyTestEngine with the LLM provider
// supplied by the caller, for tests that assert on the request the engine
// actually put on the wire rather than only on what came back.
func newPolicyTestEngineWithProvider(t *testing.T, provider llm.Provider, tier string) (*Engine, *SQLiteMemoryStore, *atomic.Int64, *atomic.Int64) {
	t.Helper()

	var reads, writes atomic.Int64
	server := mcp.NewServer(&mcp.Implementation{Name: "policy-server", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "read_thing", Description: "reads"},
		func(_ context.Context, _ *mcp.CallToolRequest, args policyToolArgs) (*mcp.CallToolResult, any, error) {
			reads.Add(1)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "read " + args.Value}},
			}, nil, nil
		})
	mcp.AddTool(server, &mcp.Tool{Name: "write_thing", Description: "writes"},
		func(_ context.Context, _ *mcp.CallToolRequest, args policyToolArgs) (*mcp.CallToolResult, any, error) {
			writes.Add(1)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "wrote " + args.Value}},
			}, nil, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	toolMgr := tool.NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	if err := toolMgr.RegisterServer(context.Background(), "policy-tool", config.ToolConfig{
		Transport:       "sse",
		URL:             ts.URL,
		AllowLoopback:   true,
		IdempotentTools: []string{"read_thing"},
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

	permissions, err := security.NewPermissionEngine(tier)
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	e := NewEngine("pamela", router, store, nil, permissions, nil, "test prompt", nil, toolMgr, nil, testLogger())
	return e, store, &reads, &writes
}

// toolCallResponse builds an LLM response requesting one tool call.
func toolCallResponse(id, name, args string) *llm.ChatResponse {
	return &llm.ChatResponse{
		ToolCalls: []llm.ToolCall{{
			ID:       id,
			Type:     "function",
			Function: llm.FunctionCall{Name: name, Arguments: args},
		}},
		FinishReason: "tool_calls",
		Model:        "test-model",
	}
}

func dryRunPolicy() ExecPolicy {
	return ExecPolicy{Kind: ExecDryRun, ConvID: "dryrun:test", AsOf: time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)}
}

func TestDryRun_SuppressesWritesAndExecutesReads(t *testing.T) {
	e, _, reads, writes := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "write_thing", `{"value":"x"}`),
		toolCallResponse("c2", "read_thing", `{"value":"y"}`),
		{Content: "all done", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	if got := writes.Load(); got != 0 {
		t.Errorf("write_thing executed %d times, want 0 — the policy must suppress non-idempotent tools", got)
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("read_thing executed %d times, want 1 — idempotent tools run for real", got)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("recorded %d tool calls, want 2", len(result.ToolCalls))
	}
	if got := result.ToolCalls[0].Outcome; got != outcomeSuppressed {
		t.Errorf("write outcome = %q, want %q", got, outcomeSuppressed)
	}
	if !result.ToolCalls[0].Success {
		t.Error("a suppressed call must not read as a failure — nothing is at fault")
	}
	if !strings.Contains(result.ToolCalls[0].Result, "write suppressed") {
		t.Errorf("suppressed result = %q, want the suppression marker", result.ToolCalls[0].Result)
	}
	if got := result.ToolCalls[1].Outcome; got != "ok" {
		t.Errorf("read outcome = %q, want ok", got)
	}
	if result.Rounds != 2 {
		t.Errorf("Rounds = %d, want 2", result.Rounds)
	}
	if result.Response != "all done" {
		t.Errorf("Response = %q, want %q", result.Response, "all done")
	}
}

// firstRequest returns the first ChatRequest the provider received, which is
// the one carrying the turn's assembled context.
func firstRequest(t *testing.T, p *capturingProvider) llm.ChatRequest {
	t.Helper()
	if len(p.requests) == 0 {
		t.Fatal("provider received no requests")
	}
	return p.requests[0]
}

// countContent reports how many messages carry exactly the given content.
func countContent(messages []llm.Message, content string) int {
	n := 0
	for _, m := range messages {
		if m.Content == content {
			n++
		}
	}
	return n
}

func TestDryRun_SendsTheTriggerMessageToTheModel(t *testing.T) {
	// A live turn's message reaches the model only because it is persisted and
	// read straight back as history. A policy turn persists nothing and loads no
	// history, so without an explicit hand-off the request is a system prompt
	// and nothing else — and a model given no prompt invents one, which is what
	// made a scheduled github-trending preview answer a question about creating
	// skills that nobody asked.
	provider := &capturingProvider{responses: []*llm.ChatResponse{
		{Content: "ok", FinishReason: "stop", Model: "test-model"},
	}}
	e, _, _, _ := newPolicyTestEngineWithProvider(t, provider, "autonomous")

	const trigger = "[Scheduled: daily-github-trending | 2026-07-06T10:00:00+10:00 Australia/Sydney | 2026-W28]"
	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{
		Text:        trigger,
		SkillName:   "daily-github-trending",
		IsScheduled: true,
	}, dryRunPolicy()); err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	msgs := firstRequest(t, provider).Messages
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != trigger {
		t.Errorf("last wire message = {%s, %q}, want {user, %q}", last.Role, last.Content, trigger)
	}
	if n := countContent(msgs, trigger); n != 1 {
		t.Errorf("trigger appeared %d times in the request, want exactly 1", n)
	}
}

func TestDryRun_HistoryFromPrecedesTheCurrentTurn(t *testing.T) {
	// HistoryFrom is the context *before* the turn, so borrowed messages come
	// first and the message being answered stays last. Getting this backwards
	// would ask the model to answer the borrowed conversation's final message.
	provider := &capturingProvider{responses: []*llm.ChatResponse{
		{Content: "ok", FinishReason: "stop", Model: "test-model"},
	}}
	e, store, _, _ := newPolicyTestEngineWithProvider(t, provider, "autonomous")

	ctx := context.Background()
	if err := store.GetOrCreateConversationByID(ctx, "chan:main", "telegram", "42"); err != nil {
		t.Fatalf("GetOrCreateConversationByID: %v", err)
	}
	for _, m := range []StoredMessage{
		{Role: "user", Content: "earlier question"},
		{Role: "assistant", Content: "earlier answer"},
	} {
		if _, err := store.AddMessage(ctx, "chan:main", m); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}

	policy := dryRunPolicy()
	policy.HistoryFrom = "chan:main"
	if _, err := e.DryRun(ctx, adapter.IncomingMessage{Text: "and now?"}, policy); err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	msgs := firstRequest(t, provider).Messages
	if len(msgs) != 4 {
		t.Fatalf("request carried %d messages, want 4 (system + 2 borrowed + the turn): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Errorf("message 0 role = %q, want system", msgs[0].Role)
	}
	// The two borrowed messages share a created_at to the second, so their
	// relative order is not the point — that they precede the turn is.
	for _, want := range []string{"earlier question", "earlier answer"} {
		if n := countContent(msgs[1:3], want); n != 1 {
			t.Errorf("borrowed message %q appeared %d times before the turn, want 1", want, n)
		}
	}
	if last := msgs[3]; last.Role != "user" || last.Content != "and now?" {
		t.Errorf("last message = {%s, %q}, want {user, \"and now?\"}", last.Role, last.Content)
	}
}

func TestLiveTurn_SendsTheUserMessageExactlyOnce(t *testing.T) {
	// The live path reads its user message back out of storage, so the explicit
	// hand-off must stay off there or every live turn would send it twice.
	provider := &capturingProvider{responses: []*llm.ChatResponse{
		{Content: "hi", FinishReason: "stop", Model: "test-model"},
	}}
	e, _, _, _ := newPolicyTestEngineWithProvider(t, provider, "autonomous")

	if _, err := e.Chat(context.Background(), adapter.IncomingMessage{
		Adapter: "api", ExternalID: "u1", Text: "hello",
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	msgs := firstRequest(t, provider).Messages
	if n := countContent(msgs, "hello"); n != 1 {
		t.Errorf("live request carried the user message %d times, want exactly 1: %+v", n, msgs)
	}
}

func TestDryRun_PersistsNothing(t *testing.T) {
	e, store, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "read_thing", `{"value":"y"}`),
		{Content: "done", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")

	policy := dryRunPolicy()
	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, policy); err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	ctx := context.Background()
	msgs, err := store.GetMessages(ctx, policy.ConvID, 50)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("stored %d messages, want 0", len(msgs))
	}

	calls, err := store.GetToolCalls(ctx, policy.ConvID)
	if err != nil {
		t.Fatalf("GetToolCalls: %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("stored %d tool calls, want 0", len(calls))
	}

	stats, err := store.GetConversationStats(ctx, policy.ConvID)
	if err == nil && stats != nil {
		t.Errorf("conversation stats were written for a dry run: %+v", stats)
	}

	convs, total, err := store.ListConversations(ctx, SessionListOpts{Limit: 50})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if total != 0 || len(convs) != 0 {
		t.Errorf("dry run created %d conversation rows, want 0", total)
	}
}

func TestDryRun_PinsClockToAsOf(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "ok", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	// A wall clock two months past as_of: a leaked e.now() would show here.
	e.now = func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) }

	policy := dryRunPolicy()
	prompt := e.buildSystemPrompt(nil, adapter.IncomingMessage{}, &policy)
	if !strings.Contains(prompt.prompt, "Today is Monday 2026-07-06") {
		t.Errorf("system prompt should carry the pinned as_of date, got:\n%s", prompt.prompt)
	}
	if strings.Contains(prompt.prompt, "2026-09-30") {
		t.Error("system prompt leaked the wall clock instead of as_of")
	}
}

func TestDryRun_UnknownToolIsSuppressed(t *testing.T) {
	// Fail closed: the idempotency allowlist is the only "safe to execute"
	// signal, so anything it does not vouch for must not run.
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "mystery_tool", `{}`),
		{Content: "done", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("recorded %d tool calls, want 1", len(result.ToolCalls))
	}
	if got := result.ToolCalls[0].Outcome; got != outcomeSuppressed {
		t.Errorf("unknown tool outcome = %q, want %q", got, outcomeSuppressed)
	}
}

func TestDryRun_SuppressedResultIsNotCached(t *testing.T) {
	// A suppressed call executed nothing, so a later identical call must not
	// be told it has a "cached result from round N" that never existed.
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "write_thing", `{"value":"x"}`),
		toolCallResponse("c2", "write_thing", `{"value":"x"}`),
		{Content: "done", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	for i, rec := range result.ToolCalls {
		if rec.Outcome != outcomeSuppressed {
			t.Errorf("call %d outcome = %q, want %q", i, rec.Outcome, outcomeSuppressed)
		}
		if strings.Contains(rec.Result, "cached result") {
			t.Errorf("call %d served a cached result for a call that never executed", i)
		}
	}
}

func TestDryRun_SupervisedTierSkipsApproval(t *testing.T) {
	// approvals is nil here, but the tier is supervised: if the policy did not
	// disable the chain, an idempotent call would still take the supervised
	// path. The assertion that matters is that the turn completes without
	// blocking and the read executed.
	e, _, reads, writes := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "read_thing", `{"value":"y"}`),
		toolCallResponse("c2", "write_thing", `{"value":"x"}`),
		{Content: "done", FinishReason: "stop", Model: "test-model"},
	}, "supervised")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("read_thing executed %d times, want 1", got)
	}
	if got := writes.Load(); got != 0 {
		t.Errorf("write_thing executed %d times, want 0", got)
	}
	if result.Response != "done" {
		t.Errorf("Response = %q, want done", result.Response)
	}
}

func TestDryRun_AuditEventsCarryPseudoIdentityAndSource(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "write_thing", `{"value":"x"}`),
		{Content: "done", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy()); err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	if len(auditor.events) == 0 {
		t.Fatal("dry run emitted no audit events — full live-turn semantics are the default")
	}
	for _, ev := range auditor.events {
		if ev.Agent != "pamela#dryrun" {
			t.Errorf("event %s/%s agent = %q, want pamela#dryrun", ev.Category, ev.Action, ev.Agent)
		}
		if ev.Source != "dryrun" {
			t.Errorf("event %s/%s source = %q, want dryrun", ev.Category, ev.Action, ev.Source)
		}
		if ev.ConversationID != "dryrun:test" {
			t.Errorf("event %s/%s conversation = %q, want dryrun:test", ev.Category, ev.Action, ev.ConversationID)
		}
	}
}

func TestDryRun_SummaryAuditDropsPerRoundChatter(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "read_thing", `{"value":"y"}`),
		{Content: "done", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	policy := dryRunPolicy()
	policy.AuditMode = AuditSummary
	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, policy); err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	for _, ev := range auditor.events {
		if ev.Status == "ok" && ev.Category != "eval" {
			t.Errorf("summary mode emitted an ok-status %s/%s event", ev.Category, ev.Action)
		}
	}
}

func TestLiveTurn_AuditEventsAreUnmarked(t *testing.T) {
	// The zero-footprint guarantee: a user who never runs a dry run sees no
	// change in the audit trail.
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "hi", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	if _, err := e.Chat(context.Background(), adapter.IncomingMessage{
		Adapter: "api", ExternalID: "u1", Text: "hello",
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if len(auditor.events) == 0 {
		t.Fatal("live turn emitted no audit events")
	}
	for _, ev := range auditor.events {
		if strings.Contains(ev.Agent, "#") {
			t.Errorf("live event %s/%s carries a pseudo-identity %q", ev.Category, ev.Action, ev.Agent)
		}
		if ev.Source == "dryrun" || ev.Source == "eval" {
			t.Errorf("live event %s/%s carries policy source %q", ev.Category, ev.Action, ev.Source)
		}
	}
}

func TestDryRun_RejectsLivePolicy(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "hi", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")

	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, ExecPolicy{ConvID: "dryrun:x"}); err == nil {
		t.Error("DryRun with a live policy should fail — it would persist a real turn")
	}
	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, ExecPolicy{Kind: ExecDryRun}); err == nil {
		t.Error("DryRun without a conversation identity should fail")
	}
}

func TestExecPolicy_AuditAgent(t *testing.T) {
	cases := []struct {
		name   string
		policy *ExecPolicy
		want   string
	}{
		{"live", nil, "pamela"},
		{"dry run", &ExecPolicy{Kind: ExecDryRun}, "pamela#dryrun"},
		{"eval variant", &ExecPolicy{Kind: ExecEval, Variant: "candidate"}, "pamela#eval:candidate"},
		{"eval incumbent", &ExecPolicy{Kind: ExecEval}, "pamela#eval"},
	}
	for _, tc := range cases {
		if got := tc.policy.auditAgent("pamela"); got != tc.want {
			t.Errorf("%s: auditAgent() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDryRun_ModelOverrideDoesNotMutateTheEngineRouter(t *testing.T) {
	// The router is shared by every turn on an agent. If an override mutated it
	// instead of cloning, a preview would silently retarget the model of a live
	// turn already in flight — the reason WithModel exists.
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "ok", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	before := e.router.DefaultModel()

	policy := dryRunPolicy()
	policy.Model = "some/other-model"
	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, policy)
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	if after := e.router.DefaultModel(); after != before {
		t.Errorf("engine router model = %q after an override, want %q unchanged", after, before)
	}
	if result.RequestedModel != "some/other-model" {
		t.Errorf("RequestedModel = %q, want the override recorded", result.RequestedModel)
	}
}

func TestRouterFor_ReturnsEngineRouterWithoutOverride(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "ok", FinishReason: "stop"},
	}, "autonomous")

	if got := e.routerFor(nil); got != e.router {
		t.Error("a live turn must use the engine's own router, not a clone")
	}
	if got := e.routerFor(&ExecPolicy{Kind: ExecDryRun}); got != e.router {
		t.Error("a policy with no model override must reuse the engine router")
	}
	clone := e.routerFor(&ExecPolicy{Kind: ExecDryRun, Model: "other/model"})
	if clone == e.router {
		t.Error("a model override must produce a distinct router")
	}
	if clone.DefaultModel() != "other/model" {
		t.Errorf("clone model = %q, want other/model", clone.DefaultModel())
	}
}
