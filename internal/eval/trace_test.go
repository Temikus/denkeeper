package eval

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
)

func newTraceStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func sampleTrace() agent.TurnTrace {
	return agent.TurnTrace{
		Agent:          "pamela",
		ConversationID: "chan:main",
		Source:         agent.TraceSourceLive,
		Model:          "claude-opus-5",
		Provider:       "anthropic",
		Rounds:         2,
		StopReason:     "max_rounds",
		Tokens:         llm.TokenUsage{Prompt: 100, Completion: 20, CachedPrompt: 40, Total: 120},
		CostUSD:        0.0125,
		LatencyMs:      4200,
		StartedAt:      time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC),
		CreatedAt:      time.Date(2026, 8, 26, 9, 0, 4, 0, time.UTC),
		Payload: agent.TracePayload{
			SystemPrompt: "you are pamela",
			History:      []agent.TraceMessage{{Role: "user", Content: "earlier"}},
			Prompt:       "what is the weather",
			Response:     "sunny",
			Rounds: []agent.TraceRound{{Round: 1, ToolCalls: []agent.TraceToolCall{
				{Tool: "web_fetch", Arguments: `{"url":"https://example.test"}`, Result: "200 OK", Outcome: "ok", DurationMs: 120},
			}}},
		},
	}
}

func TestSaveTrace_RoundTripsPayloadAndMetadata(t *testing.T) {
	store := newTraceStore(t)
	ctx := context.Background()

	if err := store.SaveTrace(ctx, sampleTrace()); err != nil {
		t.Fatalf("SaveTrace: %v", err)
	}

	rows, err := store.ListTraces(ctx, TraceFilter{})
	if err != nil {
		t.Fatalf("ListTraces: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("listed %d traces, want 1", len(rows))
	}
	row := rows[0]
	if row.Agent != "pamela" || row.ConversationID != "chan:main" || row.Source != agent.TraceSourceLive {
		t.Errorf("metadata columns wrong: %+v", row)
	}
	if row.Model != "claude-opus-5" || row.Provider != "anthropic" {
		t.Errorf("model/provider wrong: %+v", row)
	}
	if row.TokensPrompt != 100 || row.TokensCompletion != 20 || row.TokensCached != 40 || row.TokensTotal != 120 {
		t.Errorf("usage columns wrong: %+v", row)
	}
	if row.Cost != 0.0125 || row.LatencyMs != 4200 || row.Rounds != 2 || row.StopReason != "max_rounds" {
		t.Errorf("turn columns wrong: %+v", row)
	}
	if row.Bytes <= 0 {
		t.Errorf("bytes = %d, want the encoded payload size", row.Bytes)
	}
	// A list read must not haul the payload along.
	if row.Payload != "" {
		t.Errorf("list read returned a payload of %d bytes", len(row.Payload))
	}

	full, payload, err := store.GetTrace(ctx, row.ID)
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if full.ID != row.ID {
		t.Errorf("GetTrace id = %d, want %d", full.ID, row.ID)
	}
	if payload.SystemPrompt != "you are pamela" {
		t.Errorf("system prompt = %q", payload.SystemPrompt)
	}
	if len(payload.History) != 1 || payload.History[0].Content != "earlier" {
		t.Errorf("history = %+v", payload.History)
	}
	if payload.Prompt != "what is the weather" || payload.Response != "sunny" {
		t.Errorf("prompt/response = %q / %q", payload.Prompt, payload.Response)
	}
	if len(payload.Rounds) != 1 || len(payload.Rounds[0].ToolCalls) != 1 {
		t.Fatalf("rounds = %+v", payload.Rounds)
	}
	call := payload.Rounds[0].ToolCalls[0]
	if !strings.Contains(call.Arguments, "example.test") || call.Result != "200 OK" {
		t.Errorf("tool payloads lost: %+v", call)
	}
}

func TestGetTrace_MissingIsNotFound(t *testing.T) {
	store := newTraceStore(t)
	_, _, err := store.GetTrace(context.Background(), 404)
	if err == nil {
		t.Fatal("GetTrace on a missing row returned no error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want the ErrNotFound sentinel", err)
	}
}

func TestSaveTrace_DefaultsSourceAndTimestamps(t *testing.T) {
	store := newTraceStore(t)
	ctx := context.Background()

	if err := store.SaveTrace(ctx, agent.TurnTrace{Agent: "pamela", ConversationID: "c1"}); err != nil {
		t.Fatalf("SaveTrace: %v", err)
	}
	rows, err := store.ListTraces(ctx, TraceFilter{})
	if err != nil {
		t.Fatalf("ListTraces: %v", err)
	}
	if rows[0].Source != agent.TraceSourceLive {
		t.Errorf("source = %q, want %q — a stored row must never carry an empty discriminator",
			rows[0].Source, agent.TraceSourceLive)
	}
	if rows[0].CreatedAt.IsZero() || rows[0].StartedAt.IsZero() {
		t.Errorf("timestamps left zero: %+v", rows[0])
	}
}

func TestListTraces_FiltersAndOrdersNewestFirst(t *testing.T) {
	store := newTraceStore(t)
	ctx := context.Background()

	base := sampleTrace()
	for _, tr := range []agent.TurnTrace{
		{Agent: "pamela", ConversationID: "chan:main", Source: agent.TraceSourceLive, CreatedAt: base.CreatedAt},
		{Agent: "argus", ConversationID: "chan:ops", Source: agent.TraceSourceLive, CreatedAt: base.CreatedAt.Add(time.Minute)},
		{Agent: "pamela", ConversationID: "eval:1:2:0:3", Source: agent.TraceSourceEval, CreatedAt: base.CreatedAt.Add(2 * time.Minute)},
	} {
		if err := store.SaveTrace(ctx, tr); err != nil {
			t.Fatalf("SaveTrace: %v", err)
		}
	}

	all, err := store.ListTraces(ctx, TraceFilter{})
	if err != nil {
		t.Fatalf("ListTraces: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("listed %d, want 3", len(all))
	}
	if all[0].Source != agent.TraceSourceEval {
		t.Errorf("first row = %+v, want the newest (the eval trace)", all[0])
	}

	byAgent, err := store.ListTraces(ctx, TraceFilter{Agent: "pamela"})
	if err != nil {
		t.Fatalf("ListTraces(agent): %v", err)
	}
	if len(byAgent) != 2 {
		t.Errorf("agent filter returned %d rows, want 2", len(byAgent))
	}

	bySource, err := store.ListTraces(ctx, TraceFilter{Source: agent.TraceSourceLive})
	if err != nil {
		t.Fatalf("ListTraces(source): %v", err)
	}
	if len(bySource) != 2 {
		t.Errorf("source filter returned %d rows, want 2", len(bySource))
	}

	byConv, err := store.ListTraces(ctx, TraceFilter{ConversationID: "chan:ops"})
	if err != nil {
		t.Fatalf("ListTraces(conversation): %v", err)
	}
	if len(byConv) != 1 || byConv[0].Agent != "argus" {
		t.Errorf("conversation filter returned %+v", byConv)
	}

	since, err := store.ListTraces(ctx, TraceFilter{Since: base.CreatedAt.Add(90 * time.Second)})
	if err != nil {
		t.Fatalf("ListTraces(since): %v", err)
	}
	if len(since) != 1 || since[0].Source != agent.TraceSourceEval {
		t.Errorf("since filter returned %+v", since)
	}
}

func TestListTraces_PagesWithLimitAndOffset(t *testing.T) {
	store := newTraceStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := store.SaveTrace(ctx, agent.TurnTrace{Agent: "pamela", ConversationID: "c"}); err != nil {
			t.Fatalf("SaveTrace: %v", err)
		}
	}

	page, err := store.ListTraces(ctx, TraceFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListTraces: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("limit 2 returned %d rows", len(page))
	}
	next, err := store.ListTraces(ctx, TraceFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListTraces(offset): %v", err)
	}
	if len(next) != 2 || next[0].ID >= page[1].ID {
		t.Errorf("offset page did not continue the listing: %+v then %+v", page, next)
	}

	n, err := store.CountTraces(ctx, TraceFilter{})
	if err != nil {
		t.Fatalf("CountTraces: %v", err)
	}
	if n != 5 {
		t.Errorf("CountTraces = %d, want 5", n)
	}
}

func TestListTraces_LimitIsBounded(t *testing.T) {
	store := newTraceStore(t)
	ctx := context.Background()
	if err := store.SaveTrace(ctx, agent.TurnTrace{Agent: "pamela"}); err != nil {
		t.Fatalf("SaveTrace: %v", err)
	}
	// A caller asking for more than the page cap gets the cap, not an error.
	rows, err := store.ListTraces(ctx, TraceFilter{Limit: traceMaxLimit * 10})
	if err != nil {
		t.Fatalf("ListTraces: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("listed %d rows, want 1", len(rows))
	}
}

// A filtered page counted against the whole table tells a pager there is more
// behind it than the filter can ever return, so "load more" never terminates.
func TestCountTraces_HonoursTheSameFilterAsTheListing(t *testing.T) {
	store := newTraceStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := store.SaveTrace(ctx, agent.TurnTrace{Agent: "pamela", Source: agent.TraceSourceLive}); err != nil {
			t.Fatalf("SaveTrace: %v", err)
		}
	}
	if err := store.SaveTrace(ctx, agent.TurnTrace{Agent: "default", Source: agent.TraceSourceEval}); err != nil {
		t.Fatalf("SaveTrace: %v", err)
	}

	for _, tc := range []struct {
		name   string
		filter TraceFilter
		want   int
	}{
		{"unfiltered", TraceFilter{}, 4},
		{"by agent", TraceFilter{Agent: "pamela"}, 3},
		{"by source", TraceFilter{Source: agent.TraceSourceEval}, 1},
		{"no match", TraceFilter{Agent: "nobody"}, 0},
	} {
		n, err := store.CountTraces(ctx, tc.filter)
		if err != nil {
			t.Fatalf("%s: CountTraces: %v", tc.name, err)
		}
		if n != tc.want {
			t.Errorf("%s: count = %d, want %d", tc.name, n, tc.want)
		}
	}
	// Paging fields are not part of the count: they say which slice to show,
	// not how many rows exist.
	n, err := store.CountTraces(ctx, TraceFilter{Limit: 1, Offset: 2})
	if err != nil {
		t.Fatalf("CountTraces(paged): %v", err)
	}
	if n != 4 {
		t.Errorf("count with paging fields = %d, want 4", n)
	}
}

func TestBoundTraceLimit_ResolvesWhatTheListingWillUse(t *testing.T) {
	if got := BoundTraceLimit(0); got != traceDefaultLimit {
		t.Errorf("BoundTraceLimit(0) = %d, want the default %d", got, traceDefaultLimit)
	}
	if got := BoundTraceLimit(-5); got != traceDefaultLimit {
		t.Errorf("BoundTraceLimit(-5) = %d, want the default %d", got, traceDefaultLimit)
	}
	if got := BoundTraceLimit(7); got != 7 {
		t.Errorf("BoundTraceLimit(7) = %d, want 7", got)
	}
	if got := BoundTraceLimit(traceMaxLimit + 1); got != traceMaxLimit {
		t.Errorf("BoundTraceLimit(over) = %d, want the cap %d", got, traceMaxLimit)
	}
}

func TestPruneTracesBefore_DeletesOnlyOlderRows(t *testing.T) {
	store := newTraceStore(t)
	ctx := context.Background()
	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-time.Hour)

	for _, at := range []time.Time{old, old.Add(time.Hour), recent} {
		if err := store.SaveTrace(ctx, agent.TurnTrace{Agent: "pamela", CreatedAt: at}); err != nil {
			t.Fatalf("SaveTrace: %v", err)
		}
	}

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	n, err := store.PruneTracesBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneTracesBefore: %v", err)
	}
	if n != 2 {
		t.Errorf("pruned %d rows, want 2", n)
	}
	left, err := store.CountTraces(ctx, TraceFilter{})
	if err != nil {
		t.Fatalf("CountTraces: %v", err)
	}
	if left != 1 {
		t.Errorf("%d rows left, want 1", left)
	}
}

// The store is what the engine writes through, so it must satisfy the sink
// interface without an adapter in main.go.
func TestStore_ImplementsTraceSink(t *testing.T) {
	var _ agent.TraceSink = (*Store)(nil)
}
