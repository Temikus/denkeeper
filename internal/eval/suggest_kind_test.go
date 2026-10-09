package eval

import (
	"context"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
)

// kindCase is one turn shape for the Go/SQL agreement test.
type kindCase struct {
	name   string
	prompt string
	calls  []agent.ToolCallRecord
	skills []agent.SkillUsageRecord
}

func roundsOf(n int) []agent.ToolCallRecord {
	calls := make([]agent.ToolCallRecord, n)
	for i := range calls {
		calls[i] = agent.ToolCallRecord{ToolName: "kv_get", Round: i + 1, Success: true, Outcome: "ok"}
	}
	return calls
}

// TestCategoryFor_AgreesWithStoreKind pins categoryFor to the store's SQL
// classifier: a turn the ?category= filter selects must be the category
// Suggest then labels it, or a narrowed pass silently drops or mislabels it.
func TestCategoryFor_AgreesWithStoreKind(t *testing.T) {
	command := []agent.SkillUsageRecord{{SkillName: "report", MatchType: "command"}}
	cases := []kindCase{
		{name: "plain chat", prompt: "hello"},
		{name: "two rounds stays chat", prompt: "look it up", calls: roundsOf(2)},
		{name: "three calls in one round", prompt: "fan out", calls: []agent.ToolCallRecord{
			{ToolName: "a", Round: 1, Outcome: "ok"}, {ToolName: "b", Round: 1, Outcome: "ok"}, {ToolName: "c", Round: 1, Outcome: "ok"},
		}},
		{name: "three rounds", prompt: "dig", calls: roundsOf(3)},
		{name: "command", prompt: "/report", skills: command},
		{name: "command beats tool weight", prompt: "/report all", calls: roundsOf(5), skills: command},
		{name: "ambient match is not a command", prompt: "morning", skills: []agent.SkillUsageRecord{{SkillName: "soul", MatchType: "always"}}},
		{name: "scheduled skill", prompt: "[Scheduled: heartbeat | 2026-08-01T10:00:00Z UTC | 2026-W31]"},
		{name: "scheduled trigger", prompt: "[Scheduled trigger: nightly | 2026-08-01T10:00:00Z UTC]"},
		{name: "scheduled beats tool weight", prompt: "[Scheduled: digest | x]", calls: roundsOf(4)},
		{name: "lowercase is typed text", prompt: "[scheduled] not really"},
	}

	store, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	convID, _ := store.GetOrCreateConversation(ctx, "telegram", "1")
	for _, c := range cases {
		userID, err := store.AddMessage(ctx, convID, agent.StoredMessage{Role: "user", Content: c.prompt})
		if err != nil {
			t.Fatalf("%s: adding user message: %v", c.name, err)
		}
		replyID, err := store.AddMessage(ctx, convID, agent.StoredMessage{Role: "assistant", Content: "ok"})
		if err != nil {
			t.Fatalf("%s: adding reply: %v", c.name, err)
		}
		if len(c.calls) > 0 {
			if err := store.AddToolCalls(ctx, convID, replyID, c.calls); err != nil {
				t.Fatalf("%s: adding tool calls: %v", c.name, err)
			}
		}
		if len(c.skills) > 0 {
			if err := store.AddSkillUsages(ctx, convID, userID, c.skills); err != nil {
				t.Fatalf("%s: adding skill usages: %v", c.name, err)
			}
		}
	}

	since := time.Now().Add(-time.Hour)
	all, err := store.ListInterestingTurns(ctx, agent.InterestingTurnQuery{Since: since})
	if err != nil {
		t.Fatalf("ListInterestingTurns: %v", err)
	}
	if len(all) != len(cases) {
		t.Fatalf("turns = %d, want %d", len(all), len(cases))
	}
	want := map[int64]string{}
	for _, turn := range all {
		want[turn.MessageID] = categoryFor(turn)
	}

	got := map[int64]string{}
	for _, cat := range HistoryCategories() {
		turns, err := store.ListInterestingTurns(ctx, agent.InterestingTurnQuery{Since: since, Kind: cat})
		if err != nil {
			t.Fatalf("ListInterestingTurns(%s): %v", cat, err)
		}
		for _, turn := range turns {
			if prev, dup := got[turn.MessageID]; dup {
				t.Errorf("turn %q matched both %s and %s", turn.Content, prev, cat)
			}
			got[turn.MessageID] = cat
		}
	}
	for _, turn := range all {
		if got[turn.MessageID] != want[turn.MessageID] {
			t.Errorf("turn %q: SQL kind %q, categoryFor %q", turn.Content, got[turn.MessageID], want[turn.MessageID])
		}
	}
}

func TestHistoryCategories_AreTheStoreKinds(t *testing.T) {
	kinds := []string{agent.TurnKindChat, agent.TurnKindSkillCommand, agent.TurnKindScheduled, agent.TurnKindToolHeavy}
	cats := HistoryCategories()
	if len(cats) != len(kinds) {
		t.Fatalf("HistoryCategories = %v, store kinds = %v", cats, kinds)
	}
	for i := range kinds {
		if cats[i] != kinds[i] {
			t.Errorf("HistoryCategories[%d] = %q, store kind %q", i, cats[i], kinds[i])
		}
	}
}
