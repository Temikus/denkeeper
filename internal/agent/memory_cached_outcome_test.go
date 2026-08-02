package agent

import (
	"context"
	"testing"
)

func TestAddToolCalls_CachedOutcomeRoundTrips(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")
	msgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{Role: "assistant", Content: "used tools"})

	calls := []ToolCallRecord{
		{ToolName: "web_fetch", Round: 2, Success: true, Outcome: "cached", DurationMs: 0},
	}
	if err := store.AddToolCalls(ctx, "test:1", msgID, calls); err != nil {
		t.Fatalf("AddToolCalls: %v", err)
	}

	got, err := store.GetToolCalls(ctx, "test:1")
	if err != nil {
		t.Fatalf("GetToolCalls: %v", err)
	}
	if len(got) != 1 || got[0].Outcome != "cached" || !got[0].Success {
		t.Fatalf("got = %+v, want one Success=true Outcome=cached record", got)
	}
}

func TestGetTelemetrySummary_CachedOutcome(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")
	msgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{Role: "assistant", Content: "used tools"})

	calls := []ToolCallRecord{
		{ToolName: "web_fetch", ServerName: "web-default", Round: 1, Success: true, Outcome: "ok", DurationMs: 800,
			SkillName: "research", SkillVersion: "1.0"},
		{ToolName: "web_fetch", ServerName: "web-default", Round: 2, Success: true, Outcome: "cached", DurationMs: 0,
			SkillName: "research", SkillVersion: "1.0"},
	}
	if err := store.AddToolCalls(ctx, "test:1", msgID, calls); err != nil {
		t.Fatalf("AddToolCalls: %v", err)
	}

	summary, err := store.GetTelemetrySummary(ctx, nil, nil)
	if err != nil {
		t.Fatalf("GetTelemetrySummary: %v", err)
	}
	if len(summary.ByTool) != 1 {
		t.Fatalf("ByTool len = %d, want 1", len(summary.ByTool))
	}
	tu := summary.ByTool[0]
	if tu.CallCount != 2 {
		t.Errorf("CallCount = %d, want 2 (cached calls still count as calls)", tu.CallCount)
	}
	if tu.CachedCount != 1 {
		t.Errorf("CachedCount = %d, want 1", tu.CachedCount)
	}
	if tu.RejectionCount != 0 || tu.FailureCount != 0 || tu.DenialCount != 0 {
		t.Errorf("fault counts = %d/%d/%d, want 0/0/0 (a cache hit is not a fault)",
			tu.RejectionCount, tu.FailureCount, tu.DenialCount)
	}
	// 0ms cache hits must not drag the average down: only the real execution counts.
	if tu.AvgDuration != 800 {
		t.Errorf("AvgDuration = %f, want 800 (cached rows excluded)", tu.AvgDuration)
	}

	if len(summary.ByToolSkill) != 1 {
		t.Fatalf("ByToolSkill len = %d, want 1", len(summary.ByToolSkill))
	}
	ts := summary.ByToolSkill[0]
	if ts.CallCount != 2 || ts.CachedCount != 1 || ts.AvgDuration != 800 {
		t.Errorf("ByToolSkill = {CallCount:%d CachedCount:%d AvgDuration:%f}, want {2 1 800}",
			ts.CallCount, ts.CachedCount, ts.AvgDuration)
	}
}

// TestGetTelemetrySummary_AllCached guards the COALESCE on the duration
// average: a tool whose every call was served from cache has no real
// executions to average over, and must report 0 rather than fail the scan.
func TestGetTelemetrySummary_AllCached(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, _ = store.GetOrCreateConversation(ctx, "test", "1")
	msgID, _ := store.AddMessage(ctx, "test:1", StoredMessage{Role: "assistant", Content: "x"})
	if err := store.AddToolCalls(ctx, "test:1", msgID, []ToolCallRecord{
		{ToolName: "kv_get", Round: 2, Success: true, Outcome: "cached"},
	}); err != nil {
		t.Fatalf("AddToolCalls: %v", err)
	}

	summary, err := store.GetTelemetrySummary(ctx, nil, nil)
	if err != nil {
		t.Fatalf("GetTelemetrySummary: %v", err)
	}
	if len(summary.ByTool) != 1 || summary.ByTool[0].AvgDuration != 0 || summary.ByTool[0].CachedCount != 1 {
		t.Errorf("ByTool = %+v, want one row with AvgDuration 0 and CachedCount 1", summary.ByTool)
	}
}
