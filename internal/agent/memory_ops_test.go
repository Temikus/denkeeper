package agent

import (
	"context"
	"testing"
	"time"
)

func opsStore(t *testing.T) *SQLiteMemoryStore {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// opsTurn is one stored turn: the user message, its reply, the reply's tool
// calls and the user message's skill matches.
type opsTurn struct {
	agent, conv, model string
	cost               float64
	completion         int
	calls              []ToolCallRecord
	skills             []SkillUsageRecord
}

// seedOpsTurn stores a turn the way Engine.persistTelemetry does and returns
// the user and reply message ids. Unlike seedTurn it sets the reply's model.
func seedOpsTurn(t *testing.T, store *SQLiteMemoryStore, turn opsTurn) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	convID, err := store.GetOrCreateConversation(ctx, "telegram", turn.conv)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	reply := StoredMessage{Role: "assistant", Content: "reply", Cost: turn.cost, Model: turn.model, TokensCompletion: turn.completion}
	replyID, err := store.AddMessage(ctx, convID, reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.calls) > 0 {
		if err := store.AddToolCalls(ctx, convID, replyID, turn.calls); err != nil {
			t.Fatal(err)
		}
	}
	if len(turn.skills) > 0 {
		if err := store.AddSkillUsages(ctx, convID, userID, turn.skills); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpdateConversationStats(ctx, convID, turn.agent, reply, len(turn.calls), 0); err != nil {
		t.Fatal(err)
	}
	return userID, replyID
}

// backdateConv moves every row of a conversation to the given time.
func backdateConv(t *testing.T, store *SQLiteMemoryStore, conv string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	convID, err := store.GetOrCreateConversation(ctx, "telegram", conv)
	if err != nil {
		t.Fatal(err)
	}
	ts := at.UTC().Format(sqliteDatetimeLayout)
	for _, table := range []string{"messages", "tool_calls", "message_skills"} {
		if _, err := store.db.ExecContext(ctx, "UPDATE "+table+" SET created_at = ? WHERE conversation_id = ?", ts, convID); err != nil {
			t.Fatal(err)
		}
	}
}

func opsWindow() (time.Time, time.Time) {
	now := time.Now().UTC()
	return now.Add(-time.Hour), now.Add(time.Hour)
}

func call(tool, outcome string) ToolCallRecord {
	return ToolCallRecord{ToolName: tool, ServerName: "srv", Round: 1, DurationMs: 10, Success: outcome == "ok" || outcome == "cached", Outcome: outcome}
}

func skillCall(tool, outcome, skill, version string) ToolCallRecord {
	c := call(tool, outcome)
	c.SkillName, c.SkillVersion = skill, version
	return c
}

func TestOpsSpend_SumsAgentRowsInsideWindow(t *testing.T) {
	store := opsStore(t)
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m", cost: 0.5})
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c2", model: "m", cost: 0.25})
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "old", model: "m", cost: 9})
	backdateConv(t, store, "old", time.Now().Add(-48*time.Hour))
	seedOpsTurn(t, store, opsTurn{agent: "argus", conv: "c3", model: "m", cost: 7})

	from, to := opsWindow()
	got, err := store.OpsSpend(context.Background(), "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got.Turns != 2 || got.CostUSD != 0.75 {
		t.Fatalf("got %+v, want 2 turns and 0.75", got)
	}
}

// created_at has whole-second resolution, so a window ending now must still
// count a turn stored earlier in the same second.
func TestOpsSpend_CountsTurnInTheCurrentSecond(t *testing.T) {
	store := opsStore(t)
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m", cost: 0.5})
	now := time.Now()
	got, err := store.OpsSpend(context.Background(), "pamela", now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Turns != 1 {
		t.Fatalf("turns = %d, want 1", got.Turns)
	}
}

func TestOpsSpend_IgnoresReplyWithoutModel(t *testing.T) {
	store := opsStore(t)
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m", cost: 0.5})
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c2", model: "", cost: 0})
	from, to := opsWindow()
	got, err := store.OpsSpend(context.Background(), "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got.Turns != 1 {
		t.Fatalf("turns = %d, want 1", got.Turns)
	}
}

func TestOpsToolUsage_OutcomeSplit(t *testing.T) {
	store := opsStore(t)
	var calls []ToolCallRecord
	for _, o := range []string{"ok", "rejected", "failed", "denied", "approval_timeout", "supervisor_error", "cached"} {
		calls = append(calls, call("t", o))
	}
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m", calls: calls})
	from, to := opsWindow()
	got, err := store.OpsToolUsage(context.Background(), "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows", len(got))
	}
	r := got[0]
	if r.CallCount != 7 || r.RejectionCount != 1 || r.FailureCount != 1 || r.DenialCount != 1 ||
		r.ApprovalTimeoutCount != 1 || r.SupervisorErrorCount != 1 || r.CachedCount != 1 {
		t.Fatalf("got %+v", r)
	}
}

// Canary: the ops query and the telemetry summary must split outcomes the same way.
func TestOpsToolUsage_MatchesTelemetrySummaryForOneAgent(t *testing.T) {
	store := opsStore(t)
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m",
		calls: []ToolCallRecord{call("a", "ok"), call("a", "failed"), call("b", "denied"), call("b", "cached")}})
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c2", model: "m",
		calls: []ToolCallRecord{call("a", "rejected"), call("c", "approval_timeout")}})

	from, to := opsWindow()
	ops, err := store.OpsToolUsage(context.Background(), "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := store.GetTelemetrySummary(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ToolUsageSummary{}
	for _, r := range sum.ByTool {
		want[r.ToolName+"/"+r.ServerName] = r
	}
	if len(ops) != len(want) {
		t.Fatalf("ops has %d rows, summary %d", len(ops), len(want))
	}
	for _, r := range ops {
		if w := want[r.ToolName+"/"+r.ServerName]; r != w {
			t.Fatalf("%s: ops %+v, summary %+v", r.ToolName, r, w)
		}
	}
}

func TestOps_LeaveOutOtherAgentsConversation(t *testing.T) {
	store := opsStore(t)
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m", calls: []ToolCallRecord{skillCall("mine", "ok", "brief", "1")},
		skills: []SkillUsageRecord{{SkillName: "brief", MatchType: "command"}}})
	seedOpsTurn(t, store, opsTurn{agent: "argus", conv: "c2", model: "m", calls: []ToolCallRecord{skillCall("theirs", "ok", "secret", "1")},
		skills: []SkillUsageRecord{{SkillName: "secret", MatchType: "command"}}})

	from, to := opsWindow()
	tools, err := store.OpsToolUsage(context.Background(), "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].ToolName != "mine" {
		t.Fatalf("tools = %+v", tools)
	}
	skills, err := store.OpsSkillUsage(context.Background(), "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].SkillName != "brief" {
		t.Fatalf("skills = %+v", skills)
	}
}

func TestOpsSkillUsage_RunOwnership(t *testing.T) {
	store := opsStore(t)
	// Scheduled skill plus an ambient match: the scheduled one owns the turn.
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m", cost: 0.1, skills: []SkillUsageRecord{
		{SkillName: "heartbeat", MatchType: "schedule"}, {SkillName: "tone", MatchType: "always"}}})
	// A lone match owns its turn.
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c2", model: "m", cost: 0.2, skills: []SkillUsageRecord{
		{SkillName: "brief", MatchType: "command"}}})
	// Two non-schedule matches: nobody owns it.
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c3", model: "m", cost: 0.4, skills: []SkillUsageRecord{
		{SkillName: "brief", MatchType: "command"}, {SkillName: "tone", MatchType: "always"}}})

	from, to := opsWindow()
	got, err := store.OpsSkillUsage(context.Background(), "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string]int{}
	for _, s := range got {
		runs[s.SkillName] = s.Runs
	}
	if runs["heartbeat"] != 1 || runs["brief"] != 1 || runs["tone"] != 0 {
		t.Fatalf("runs = %v", runs)
	}
}

func TestOpsSkillUsage_CostFromTheMatchedReply(t *testing.T) {
	store := opsStore(t)
	ctx := context.Background()
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m", cost: 0.3, completion: 120,
		skills: []SkillUsageRecord{{SkillName: "brief", MatchType: "command"}}})
	// A later, unrelated turn in the same conversation.
	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "c1")
	_, _ = store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "later"})
	_, _ = store.AddMessage(ctx, convID, StoredMessage{Role: "assistant", Content: "later", Model: "m", Cost: 5, TokensCompletion: 9000})

	from, to := opsWindow()
	got, err := store.OpsSkillUsage(ctx, "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CostUSD != 0.3 || got[0].CompletionTokens != 120 || got[0].Runs != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestOpsSkillUsage_SumsToolOutcomesAcrossVersions(t *testing.T) {
	store := opsStore(t)
	seedOpsTurn(t, store, opsTurn{agent: "pamela", conv: "c1", model: "m", calls: []ToolCallRecord{
		skillCall("a", "ok", "brief", "1"), skillCall("a", "failed", "brief", "2"), skillCall("b", "denied", "brief", "2")}})
	from, to := opsWindow()
	got, err := store.OpsSkillUsage(context.Background(), "pamela", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ToolCallCount != 3 || got[0].FailureCount != 1 || got[0].DenialCount != 1 || got[0].Runs != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestOps_EmptyAgentIsError(t *testing.T) {
	store := opsStore(t)
	ctx := context.Background()
	from, to := opsWindow()
	if _, err := store.OpsSpend(ctx, "", from, to); err == nil {
		t.Error("OpsSpend accepted an empty agent")
	}
	if _, err := store.OpsToolUsage(ctx, "", from, to); err == nil {
		t.Error("OpsToolUsage accepted an empty agent")
	}
	if _, err := store.OpsSkillUsage(ctx, "", from, to); err == nil {
		t.Error("OpsSkillUsage accepted an empty agent")
	}
}
