package audit

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestInsertAndList(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	ev := Event{
		Timestamp:      now,
		Category:       CategoryToolCall,
		Action:         "execute",
		Agent:          "default",
		Summary:        "Executed tool weather_lookup",
		Detail:         `{"tool":"weather_lookup","server":"weather-mcp"}`,
		Status:         StatusOK,
		DurationMs:     150,
		Source:         "engine",
		ConversationID: "conv-1",
	}

	if err := store.Insert(ctx, ev); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	events, total, err := store.List(ctx, ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Category != CategoryToolCall {
		t.Errorf("expected category %q, got %q", CategoryToolCall, events[0].Category)
	}
	if events[0].Summary != "Executed tool weather_lookup" {
		t.Errorf("unexpected summary: %q", events[0].Summary)
	}
}

func TestInsertBatch(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	events := []Event{
		{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "tool 1", Status: StatusOK},
		{Timestamp: now, Category: CategoryLLM, Action: "complete", Summary: "llm call", Status: StatusOK},
		{Timestamp: now, Category: CategoryApproval, Action: "approve", Summary: "approved tool", Status: StatusOK},
	}

	if err := store.InsertBatch(ctx, events); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	_, total, err := store.List(ctx, ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected total=3, got %d", total)
	}
}

func TestListFilterByCategory(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "tool", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryLLM, Action: "complete", Summary: "llm", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "tool2", Status: StatusError})

	events, total, err := store.List(ctx, ListOpts{Categories: []string{CategoryToolCall}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestListFilterByStatus(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "ok", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "err", Status: StatusError})

	events, total, err := store.List(ctx, ListOpts{Statuses: []string{StatusError}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if events[0].Summary != "err" {
		t.Errorf("unexpected summary: %q", events[0].Summary)
	}
}

func TestListFilterByMultipleCategories(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "tool", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryLLM, Action: "complete", Summary: "llm", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryApproval, Action: "approve", Summary: "approval", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategorySkill, Action: "match", Summary: "skill", Status: StatusOK})

	events, total, err := store.List(ctx, ListOpts{Categories: []string{CategoryToolCall, CategoryLLM, CategoryApproval}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected total=3, got %d", total)
	}
	for _, ev := range events {
		if ev.Category == CategorySkill {
			t.Errorf("skill event leaked into a tool_call/llm/approval filter")
		}
	}
}

func TestListFilterByMultipleStatuses(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "ok", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "err", Status: StatusError})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "denied", Status: StatusDenied})

	_, total, err := store.List(ctx, ListOpts{Statuses: []string{StatusError, StatusDenied}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
}

// An empty slice must behave like the "All" chip: no condition at all, not an
// IN () that matches nothing.
func TestListFilterEmptySlicesMatchEverything(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "tool", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryLLM, Action: "complete", Summary: "llm", Status: StatusError})

	_, total, err := store.List(ctx, ListOpts{Categories: []string{}, Statuses: []string{}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
}

func TestListFilterBySearch(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "Executed weather_lookup", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryLLM, Action: "complete", Summary: "LLM call to claude", Status: StatusOK})

	events, total, err := store.List(ctx, ListOpts{Search: "weather"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestListFilterBySinceUntil(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()

	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	_ = store.Insert(ctx, Event{Timestamp: old, Category: CategoryToolCall, Action: "execute", Summary: "old", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: recent, Category: CategoryToolCall, Action: "execute", Summary: "recent", Status: StatusOK})

	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	events, total, err := store.List(ctx, ListOpts{Since: &since})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total=1, got %d", total)
	}
	if events[0].Summary != "recent" {
		t.Errorf("expected recent event, got %q", events[0].Summary)
	}
}

func TestListPagination(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	for i := 0; i < 10; i++ {
		_ = store.Insert(ctx, Event{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Category:  CategoryToolCall,
			Action:    "execute",
			Summary:   "event",
			Status:    StatusOK,
		})
	}

	events, total, err := store.List(ctx, ListOpts{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 10 {
		t.Fatalf("expected total=10, got %d", total)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Second page.
	events2, _, err := store.List(ctx, ListOpts{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(events2) != 3 {
		t.Fatalf("expected 3 events on page 2, got %d", len(events2))
	}
}

func TestPruneBefore(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()

	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Now().UTC()

	_ = store.Insert(ctx, Event{Timestamp: old, Category: CategoryToolCall, Action: "execute", Summary: "old", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: recent, Category: CategoryToolCall, Action: "execute", Summary: "recent", Status: StatusOK})

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	n, err := store.PruneBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}

	_, total, _ := store.List(ctx, ListOpts{})
	if total != 1 {
		t.Fatalf("expected 1 remaining, got %d", total)
	}
}

func TestStats(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	now := time.Now().UTC()

	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "t1", Status: StatusOK})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryToolCall, Action: "execute", Summary: "t2", Status: StatusError})
	_ = store.Insert(ctx, Event{Timestamp: now, Category: CategoryLLM, Action: "complete", Summary: "l1", Status: StatusOK})

	stats, err := store.Stats(ctx, StatsOpts{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("expected total=3, got %d", stats.Total)
	}
	if stats.ByCategory[CategoryToolCall] != 2 {
		t.Errorf("expected 2 tool_call, got %d", stats.ByCategory[CategoryToolCall])
	}
	if stats.ByCategory[CategoryLLM] != 1 {
		t.Errorf("expected 1 llm, got %d", stats.ByCategory[CategoryLLM])
	}
	if stats.ByStatus[StatusOK] != 2 {
		t.Errorf("expected 2 ok, got %d", stats.ByStatus[StatusOK])
	}
	if stats.ByStatus[StatusError] != 1 {
		t.Errorf("expected 1 error, got %d", stats.ByStatus[StatusError])
	}
	if stats.EventsLastHour != 3 {
		t.Errorf("expected events_last_hour=3, got %d", stats.EventsLastHour)
	}
}

func TestInsertBatchEmpty(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	if err := store.InsertBatch(context.Background(), nil); err != nil {
		t.Fatalf("InsertBatch(nil): %v", err)
	}
}

// seedExclusionStore inserts a realistic mix: two ordinary engine events plus
// the per-round events a dry run and an eval sample produce under the *same*
// categories, which is exactly why source is the only usable discriminator.
func seedExclusionStore(t *testing.T) (*SQLiteStore, context.Context) {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	events := []Event{
		{Timestamp: now, Category: CategoryLLM, Action: "complete", Agent: "pamela", Summary: "live", Status: StatusOK, Source: "engine"},
		{Timestamp: now, Category: CategoryToolCall, Action: "execute", Agent: "pamela", Summary: "live tool", Status: StatusOK, Source: "engine"},
		{Timestamp: now, Category: CategoryLLM, Action: "complete", Agent: "pamela#dryrun", Summary: "preview", Status: StatusOK, Source: "dryrun"},
		{Timestamp: now, Category: CategoryToolCall, Action: "execute", Agent: "pamela#dryrun", Summary: "preview tool", Status: StatusOK, Source: "dryrun"},
		{Timestamp: now, Category: CategoryLLM, Action: "complete", Agent: "pamela#eval:candidate", Summary: "sample", Status: StatusOK, Source: "eval"},
	}
	if err := store.InsertBatch(ctx, events); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	return store, ctx
}

func TestList_ExcludeSources(t *testing.T) {
	store, ctx := seedExclusionStore(t)

	events, total, err := store.List(ctx, ListOpts{ExcludeSources: []string{"dryrun", "eval"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (only the live events survive)", total)
	}
	for _, ev := range events {
		if ev.Source != "engine" {
			t.Errorf("event %q leaked through with source %q", ev.Summary, ev.Source)
		}
	}
}

func TestList_ExcludeSourcesSingle(t *testing.T) {
	store, ctx := seedExclusionStore(t)

	_, total, err := store.List(ctx, ListOpts{ExcludeSources: []string{"dryrun"}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (two live plus the eval sample)", total)
	}
}

func TestList_AgentExactMatchExcludesPseudoIdentity(t *testing.T) {
	// The pseudo-identity gives per-agent queries implicit exclusion with no
	// API change at all — that is half the point of the "#" form.
	store, ctx := seedExclusionStore(t)

	events, total, err := store.List(ctx, ListOpts{Agent: "pamela"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	for _, ev := range events {
		if strings.Contains(ev.Agent, "#") {
			t.Errorf("?agent=pamela returned pseudo-identity %q", ev.Agent)
		}
	}
}

func TestStats_ExcludeSources(t *testing.T) {
	store, ctx := seedExclusionStore(t)

	before, err := store.Stats(ctx, StatsOpts{ExcludeSources: []string{"dryrun", "eval"}})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if before.Total != 2 {
		t.Errorf("Total = %d, want 2 — a preview must not inflate the headline count", before.Total)
	}
	if got := before.ByCategory[CategoryLLM]; got != 1 {
		t.Errorf("ByCategory[llm] = %d, want 1", got)
	}
	if got := before.ByCategory[CategoryToolCall]; got != 1 {
		t.Errorf("ByCategory[tool_call] = %d, want 1", got)
	}
	if before.EventsLastHour != 2 {
		t.Errorf("EventsLastHour = %d, want 2 — the last-hour window needs the same exclusions", before.EventsLastHour)
	}

	unfiltered, err := store.Stats(ctx, StatsOpts{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if unfiltered.Total != 5 {
		t.Errorf("unfiltered Total = %d, want 5 — the record itself stays complete", unfiltered.Total)
	}
}
