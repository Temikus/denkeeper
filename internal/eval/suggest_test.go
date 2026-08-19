package eval

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
)

func TestSavedTaskSources_OnlyTurnsWithASourceMessage(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	set, err := store.CreateTaskSet(ctx, "golden", "")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	msgID := int64(77)
	for _, task := range []Task{
		{Prompt: "from a turn", Category: CategoryChat, SourceConversationID: "chan:main", SourceMessageID: &msgID},
		{Prompt: "hand written", Category: CategoryChat},
	} {
		if _, err := store.AddTask(ctx, set.ID, task); err != nil {
			t.Fatalf("AddTask: %v", err)
		}
	}

	saved, err := store.SavedTaskSources(ctx)
	if err != nil {
		t.Fatalf("SavedTaskSources: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("saved = %v, want exactly the one task with a source turn", saved)
	}
	if _, ok := saved[SourceKey("chan:main", msgID)]; !ok {
		t.Errorf("saved = %v, want key %q", saved, SourceKey("chan:main", msgID))
	}
}

// turn builds an InterestingTurn with sane defaults; the callers set only the
// facts the case is about.
func turn(id int64, content string, mutate func(*agent.InterestingTurn)) agent.InterestingTurn {
	t := agent.InterestingTurn{
		MessageID:      id,
		ConversationID: "chan:main",
		Content:        content,
		CreatedAt:      time.Date(2026, 8, 1, 0, 0, int(id), 0, time.UTC),
	}
	if mutate != nil {
		mutate(&t)
	}
	return t
}

func candidateCategories(candidates []Candidate) map[string]int {
	counts := map[string]int{}
	for _, c := range candidates {
		counts[c.Category]++
	}
	return counts
}

func TestSuggest_DropsTurnsWithNoSignal(t *testing.T) {
	turns := []agent.InterestingTurn{
		turn(1, "how are you", nil),
		turn(2, "fix it", func(x *agent.InterestingTurn) { x.Faults = 1 }),
	}

	got := Suggest(turns, SuggestOpts{Limit: 20})
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1 — an unremarkable turn teaches the set nothing: %+v", len(got), got)
	}
	if got[0].MessageID != 2 {
		t.Errorf("MessageID = %d, want 2", got[0].MessageID)
	}
	if !slices.Contains(got[0].Signals, SignalToolFault) {
		t.Errorf("Signals = %v, want %s", got[0].Signals, SignalToolFault)
	}
}

func TestSuggest_CategorisesEachKind(t *testing.T) {
	turns := []agent.InterestingTurn{
		turn(1, "run the report", func(x *agent.InterestingTurn) { x.CommandMatches = 1 }),
		turn(2, "[Scheduled: heartbeat | 2026-08-01T10:00:00Z UTC | 2026-W31]", func(x *agent.InterestingTurn) {
			x.Faults = 1
		}),
		turn(3, "dig through the logs", func(x *agent.InterestingTurn) { x.ToolCalls = 5; x.MaxRound = 4 }),
		turn(4, "write me a poem", func(x *agent.InterestingTurn) { x.ReplyCost = 1.5 }),
	}

	got := Suggest(turns, SuggestOpts{Limit: 20})
	if len(got) != 4 {
		t.Fatalf("candidates = %d, want 4: %+v", len(got), got)
	}
	want := map[int64]string{
		1: CategorySkillCommand,
		2: CategoryScheduled,
		3: CategoryToolHeavy,
		4: CategoryChat,
	}
	for _, c := range got {
		if want[c.MessageID] != c.Category {
			t.Errorf("message %d category = %q, want %q", c.MessageID, c.Category, want[c.MessageID])
		}
	}
}

func TestSuggest_CommandMatchBeatsToolWeight(t *testing.T) {
	// A command-driven turn that also ran many tools is a skill_command case:
	// the command is what the turn *was*, tool weight is only how it went.
	turns := []agent.InterestingTurn{
		turn(1, "/report", func(x *agent.InterestingTurn) { x.CommandMatches = 1; x.ToolCalls = 9; x.MaxRound = 5 }),
	}

	got := Suggest(turns, SuggestOpts{Limit: 4})
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Category != CategorySkillCommand {
		t.Errorf("Category = %q, want %q", got[0].Category, CategorySkillCommand)
	}
	want := []string{SignalManyRounds, SignalCommandSkill}
	if !slices.Equal(got[0].Signals, want) {
		t.Errorf("Signals = %v, want %v", got[0].Signals, want)
	}
}

func TestSuggest_StratifiesRatherThanRankingOverall(t *testing.T) {
	// Eight tool-heavy failures and one of each other kind. Ranked overall the
	// failures would take every slot; stratified they cannot.
	var turns []agent.InterestingTurn
	for i := int64(1); i <= 8; i++ {
		turns = append(turns, turn(i, "flaily", func(x *agent.InterestingTurn) {
			x.Faults = 2
			x.MaxRound = 5
			x.ToolCalls = 6
			x.ReplyCost = 9
		}))
	}
	turns = append(turns,
		turn(20, "/report", func(x *agent.InterestingTurn) { x.CommandMatches = 1 }),
		turn(21, "[Scheduled: heartbeat | x | y]", func(x *agent.InterestingTurn) { x.Faults = 1 }),
		turn(22, "just chatting", func(x *agent.InterestingTurn) { x.ReplyCost = 20 }),
	)

	got := Suggest(turns, SuggestOpts{Limit: 4})
	if len(got) != 4 {
		t.Fatalf("candidates = %d, want 4", len(got))
	}
	counts := candidateCategories(got)
	for _, cat := range Categories() {
		if counts[cat] != 1 {
			t.Errorf("category %q = %d candidates, want 1 (stratified, not top-4 overall): %v", cat, counts[cat], counts)
		}
	}
}

func TestSuggest_LeftoverSlotsGoToCategoriesWithSurplus(t *testing.T) {
	// Only two categories have anything; a thin category must not cost the
	// total its slots.
	turns := []agent.InterestingTurn{
		turn(1, "a", func(x *agent.InterestingTurn) { x.Faults = 1; x.MaxRound = 4 }),
		turn(2, "b", func(x *agent.InterestingTurn) { x.Faults = 1; x.MaxRound = 4 }),
		turn(3, "c", func(x *agent.InterestingTurn) { x.Faults = 1; x.MaxRound = 4 }),
		turn(4, "/one", func(x *agent.InterestingTurn) { x.CommandMatches = 1 }),
		turn(5, "/two", func(x *agent.InterestingTurn) { x.CommandMatches = 1 }),
	}

	got := Suggest(turns, SuggestOpts{Limit: 4})
	if len(got) != 4 {
		t.Fatalf("candidates = %d, want 4 — spare slots should be redistributed: %+v", len(got), got)
	}
	counts := candidateCategories(got)
	if counts[CategoryToolHeavy] != 2 || counts[CategorySkillCommand] != 2 {
		t.Errorf("counts = %v, want 2 tool_heavy and 2 skill_command", counts)
	}
}

func TestSuggest_RanksBySignalCountThenRecency(t *testing.T) {
	turns := []agent.InterestingTurn{
		turn(1, "one signal", func(x *agent.InterestingTurn) { x.MaxRound = 3 }),
		turn(2, "two signals", func(x *agent.InterestingTurn) { x.MaxRound = 3; x.Faults = 1 }),
		turn(3, "one signal, newer", func(x *agent.InterestingTurn) { x.MaxRound = 3 }),
	}

	got := Suggest(turns, SuggestOpts{Limit: 8})
	if len(got) != 3 {
		t.Fatalf("candidates = %d, want 3", len(got))
	}
	order := []int64{got[0].MessageID, got[1].MessageID, got[2].MessageID}
	want := []int64{2, 3, 1}
	if !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v (score desc, then newest first)", order, want)
	}
}

func TestSuggest_TopDecileCostIsRelativeToThePool(t *testing.T) {
	// Twenty turns, costs 1..20. The top decile is the two priciest.
	var turns []agent.InterestingTurn
	for i := int64(1); i <= 20; i++ {
		cost := float64(i)
		turns = append(turns, turn(i, "chat", func(x *agent.InterestingTurn) { x.ReplyCost = cost }))
	}

	got := Suggest(turns, SuggestOpts{Limit: 20})
	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2 — only the top decile carries a signal here: %+v", len(got), got)
	}
	for _, c := range got {
		if c.MessageID != 19 && c.MessageID != 20 {
			t.Errorf("message %d in the top decile, want only 19 and 20", c.MessageID)
		}
	}
}

func TestSuggest_FreePoolNeverEarnsTheCostSignal(t *testing.T) {
	turns := []agent.InterestingTurn{
		turn(1, "a", nil),
		turn(2, "b", nil),
	}

	if got := Suggest(turns, SuggestOpts{Limit: 20}); len(got) != 0 {
		t.Errorf("candidates = %+v, want none — a zero-cost pool has no expensive reply", got)
	}
}

func TestSuggest_SkipsTurnsAlreadySavedAsTasks(t *testing.T) {
	turns := []agent.InterestingTurn{
		turn(1, "already saved", func(x *agent.InterestingTurn) { x.Faults = 1 }),
		turn(2, "still open", func(x *agent.InterestingTurn) { x.Faults = 1 }),
	}
	exclude := map[string]struct{}{SourceKey("chan:main", 1): {}}

	got := Suggest(turns, SuggestOpts{Limit: 20, Exclude: exclude})
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1 — an accepted suggestion must not resurface: %+v", len(got), got)
	}
	if got[0].MessageID != 2 {
		t.Errorf("MessageID = %d, want 2", got[0].MessageID)
	}
}

func TestSuggest_CarriesPrecedingContext(t *testing.T) {
	turns := []agent.InterestingTurn{
		turn(1, "and then?", func(x *agent.InterestingTurn) {
			x.Faults = 1
			x.Preceding = []agent.TurnMessage{
				{Role: "user", Content: "what did I ask"},
				{Role: "assistant", Content: "you asked about X"},
			}
		}),
	}

	got := Suggest(turns, SuggestOpts{Limit: 20})
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	want := []PrecedingMessage{
		{Role: "user", Content: "what did I ask"},
		{Role: "assistant", Content: "you asked about X"},
	}
	if !slices.Equal(got[0].Preceding, want) {
		t.Errorf("Preceding = %+v, want %+v", got[0].Preceding, want)
	}
}
