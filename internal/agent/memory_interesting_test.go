package agent

import (
	"context"
	"testing"
	"time"
)

// seedTurn writes a user message, its assistant reply, and the reply's tool
// calls, returning the user message id. It mirrors what Engine.persistTelemetry
// does: tool calls hang off the assistant message, skill usages off the user's.
func seedTurn(t *testing.T, store *SQLiteMemoryStore, convID, agent, prompt string, replyCost float64, calls []ToolCallRecord, skills []SkillUsageRecord) int64 {
	t.Helper()
	ctx := context.Background()

	userID, err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: prompt})
	if err != nil {
		t.Fatalf("adding user message: %v", err)
	}
	reply := StoredMessage{Role: "assistant", Content: "reply to " + prompt, Cost: replyCost}
	replyID, err := store.AddMessage(ctx, convID, reply)
	if err != nil {
		t.Fatalf("adding assistant message: %v", err)
	}
	if len(calls) > 0 {
		if err := store.AddToolCalls(ctx, convID, replyID, calls); err != nil {
			t.Fatalf("adding tool calls: %v", err)
		}
	}
	if len(skills) > 0 {
		if err := store.AddSkillUsages(ctx, convID, userID, skills); err != nil {
			t.Fatalf("adding skill usages: %v", err)
		}
	}
	if err := store.UpdateConversationStats(ctx, convID, agent, reply, len(calls), 0); err != nil {
		t.Fatalf("updating conversation stats: %v", err)
	}
	return userID
}

func TestListInterestingTurns_CarriesReplyTelemetry(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "1")
	userID := seedTurn(t, store, convID, "pamela", "check the calendar", 0.42,
		[]ToolCallRecord{
			{ToolName: "kv_get", Round: 1, Success: true, Outcome: "ok"},
			{ToolName: "web_fetch", Round: 2, Success: false, Outcome: "rejected"},
			{ToolName: "web_fetch", Round: 3, Success: false, Outcome: "failed"},
			{ToolName: "kv_set", Round: 3, Success: true, Outcome: "cached"},
		},
		[]SkillUsageRecord{{SkillName: "calendar", MatchType: "command"}})

	turns, err := store.ListInterestingTurns(ctx, "pamela", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListInterestingTurns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1: %+v", len(turns), turns)
	}
	got := turns[0]
	if got.MessageID != userID {
		t.Errorf("MessageID = %d, want %d (the user turn, not the reply)", got.MessageID, userID)
	}
	if got.ConversationID != convID {
		t.Errorf("ConversationID = %q, want %q", got.ConversationID, convID)
	}
	if got.Content != "check the calendar" {
		t.Errorf("Content = %q, want the user prompt", got.Content)
	}
	if got.ToolCalls != 4 {
		t.Errorf("ToolCalls = %d, want 4", got.ToolCalls)
	}
	if got.MaxRound != 3 {
		t.Errorf("MaxRound = %d, want 3", got.MaxRound)
	}
	if got.Faults != 2 {
		t.Errorf("Faults = %d, want 2 (rejected + failed, not cached)", got.Faults)
	}
	if got.ReplyCost != 0.42 {
		t.Errorf("ReplyCost = %v, want 0.42", got.ReplyCost)
	}
	if got.CommandMatches != 1 {
		t.Errorf("CommandMatches = %d, want 1", got.CommandMatches)
	}
}

func TestListInterestingTurns_IgnoresAmbientAndScheduledMatches(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "1")
	seedTurn(t, store, convID, "pamela", "morning", 0.01, nil, []SkillUsageRecord{
		{SkillName: "soul", MatchType: "always"},
		{SkillName: "heartbeat", MatchType: "schedule"},
	})

	turns, err := store.ListInterestingTurns(ctx, "pamela", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListInterestingTurns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(turns))
	}
	if turns[0].CommandMatches != 0 {
		t.Errorf("CommandMatches = %d, want 0 — only match_type 'command' counts", turns[0].CommandMatches)
	}
}

func TestListInterestingTurns_ScopesToAgent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	mine, _ := store.GetOrCreateConversation(ctx, "telegram", "mine")
	theirs, _ := store.GetOrCreateConversation(ctx, "telegram", "theirs")
	seedTurn(t, store, mine, "pamela", "mine", 0.01, nil, nil)
	seedTurn(t, store, theirs, "argus", "theirs", 0.01, nil, nil)

	turns, err := store.ListInterestingTurns(ctx, "pamela", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListInterestingTurns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(turns))
	}
	if turns[0].Content != "mine" {
		t.Errorf("Content = %q, want %q — another agent's turns must never appear", turns[0].Content, "mine")
	}

	all, err := store.ListInterestingTurns(ctx, "", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListInterestingTurns(all agents): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("unfiltered turns = %d, want 2", len(all))
	}
}

func TestListInterestingTurns_CarriesPrecedingContext(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "1")
	for _, p := range []string{"first", "second", "third"} {
		seedTurn(t, store, convID, "pamela", p, 0.01, nil, nil)
	}

	turns, err := store.ListInterestingTurns(ctx, "pamela", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListInterestingTurns: %v", err)
	}
	if len(turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(turns))
	}
	// Newest first, so turns[0] is "third" and sees the two full turns before it.
	latest := turns[0]
	if latest.Content != "third" {
		t.Fatalf("first turn = %q, want %q (newest first)", latest.Content, "third")
	}
	if len(latest.Preceding) != precedingTurns {
		t.Fatalf("Preceding = %d messages, want %d: %+v", len(latest.Preceding), precedingTurns, latest.Preceding)
	}
	want := []TurnMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "reply to first"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "reply to second"},
	}
	for i, w := range want {
		if latest.Preceding[i] != w {
			t.Errorf("Preceding[%d] = %+v, want %+v (oldest first)", i, latest.Preceding[i], w)
		}
	}
	// The oldest turn has nothing before it.
	if len(turns[2].Preceding) != 0 {
		t.Errorf("oldest turn Preceding = %+v, want none", turns[2].Preceding)
	}
}

func TestListInterestingTurns_CapsPrecedingContent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "1")
	huge := ""
	for range precedingContentMax + 500 {
		huge += "x"
	}
	seedTurn(t, store, convID, "pamela", huge, 0.01, nil, nil)
	seedTurn(t, store, convID, "pamela", "next", 0.01, nil, nil)

	turns, err := store.ListInterestingTurns(ctx, "pamela", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListInterestingTurns: %v", err)
	}
	latest := turns[0]
	if len(latest.Preceding) == 0 {
		t.Fatalf("expected preceding context")
	}
	if got := []rune(latest.Preceding[0].Content); len(got) != precedingContentMax+1 {
		t.Errorf("preceding content = %d runes, want %d plus the ellipsis", len(got), precedingContentMax)
	}
	// The candidate's own prompt is never truncated — it becomes the test case.
	if len([]rune(latest.Content)) != 4 {
		t.Errorf("Content = %q, want the untruncated prompt", latest.Content)
	}
}

func TestListInterestingTurns_SkipsUnansweredTurns(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "1")
	seedTurn(t, store, convID, "pamela", "answered", 0.01, nil, nil)
	if _, err := store.AddMessage(ctx, convID, StoredMessage{Role: "user", Content: "still waiting"}); err != nil {
		t.Fatalf("adding message: %v", err)
	}

	turns, err := store.ListInterestingTurns(ctx, "pamela", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListInterestingTurns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1 — a turn with no reply has no telemetry to judge", len(turns))
	}
	if turns[0].Content != "answered" {
		t.Errorf("Content = %q, want %q", turns[0].Content, "answered")
	}
}

func TestListInterestingTurns_HonoursSince(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "1")
	oldID := seedTurn(t, store, convID, "pamela", "ancient", 0.01, nil, nil)
	seedTurn(t, store, convID, "pamela", "recent", 0.01, nil, nil)
	if _, err := store.db.ExecContext(ctx,
		`UPDATE messages SET created_at = datetime('now', '-120 days') WHERE id = ?`, oldID); err != nil {
		t.Fatalf("backdating message: %v", err)
	}

	turns, err := store.ListInterestingTurns(ctx, "pamela", time.Now().Add(-90*24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListInterestingTurns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(turns))
	}
	if turns[0].Content != "recent" {
		t.Errorf("Content = %q, want %q", turns[0].Content, "recent")
	}
}
