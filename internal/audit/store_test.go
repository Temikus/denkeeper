package audit

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
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

	// The recent row plus the prune checkpoint.
	_, total, _ := store.List(ctx, ListOpts{})
	if total != 2 {
		t.Fatalf("expected 2 remaining, got %d", total)
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

// seedDetailStore inserts one event per detail in timestamp order. List
// returns newest first, so callers index in reverse.
func seedDetailStore(t *testing.T, details ...string) *SQLiteStore {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Now().UTC()
	for i, d := range details {
		ev := Event{Timestamp: base.Add(time.Duration(i) * time.Second), Category: CategorySupervisor, Status: StatusOK, Detail: d}
		if err := store.Insert(context.Background(), ev); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	return store
}

func TestList_DetailMaxCharsTruncates(t *testing.T) {
	long := strings.Repeat("a", 100)
	multibyte := strings.Repeat("é", 20) // 20 runes, 40 bytes
	store := seedDetailStore(t, long, multibyte, "short")

	events, _, err := store.List(context.Background(), ListOpts{DetailMaxChars: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	if got, want := events[0].Detail, "short"; got != want {
		t.Errorf("under-limit detail = %q, want %q", got, want)
	}
	if got, want := events[1].Detail, strings.Repeat("é", 10)+"…[truncated, 20 chars total]"; got != want {
		t.Errorf("multibyte detail = %q, want %q", got, want)
	}
	if !utf8.ValidString(events[1].Detail) {
		t.Errorf("multibyte detail is not valid UTF-8: %q", events[1].Detail)
	}
	if got, want := events[2].Detail, strings.Repeat("a", 10)+"…[truncated, 100 chars total]"; got != want {
		t.Errorf("long detail = %q, want %q", got, want)
	}
}

func TestList_DetailMaxCharsAbsentLeavesDetail(t *testing.T) {
	long := strings.Repeat("x", 5000)
	store := seedDetailStore(t, long)

	events, _, err := store.List(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 || events[0].Detail != long {
		t.Fatalf("detail changed without DetailMaxChars")
	}
}

func TestList_DetailMaxCharsExactLengthUntouched(t *testing.T) {
	store := seedDetailStore(t, "0123456789")

	events, _, err := store.List(context.Background(), ListOpts{DetailMaxChars: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if events[0].Detail != "0123456789" {
		t.Errorf("detail at exactly the limit was changed: %q", events[0].Detail)
	}
}

// --- prune and the checkpoint row ---

var (
	pruneOld    = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	pruneCutoff = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
)

func eventAt(ts time.Time, summary string) Event {
	return Event{Timestamp: ts, Category: CategoryToolCall, Action: "execute", Summary: summary, Status: StatusOK}
}

func insertAt(t *testing.T, s *SQLiteStore, events ...Event) {
	t.Helper()
	if err := s.InsertBatch(context.Background(), events); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
}

type checkpointDetail struct {
	PrunedRows        int64  `json:"pruned_rows"`
	PrunedThroughID   int64  `json:"pruned_through_id"`
	PrunedThroughHash string `json:"pruned_through_hash"`
	Cutoff            string `json:"cutoff"`
}

func checkpoints(t *testing.T, s *SQLiteStore) []checkpointDetail {
	t.Helper()
	var details []string
	if err := s.db.Select(&details, `SELECT detail FROM audit_events WHERE category = ? AND action = ? ORDER BY id`,
		CategoryAudit, ActionPruneCheckpoint); err != nil {
		t.Fatal(err)
	}
	out := make([]checkpointDetail, len(details))
	for i, d := range details {
		var raw map[string]any
		if err := json.Unmarshal([]byte(d), &raw); err != nil {
			t.Fatalf("checkpoint detail %q: %v", d, err)
		}
		for _, k := range []string{"pruned_rows", "pruned_through_id", "pruned_through_hash", "cutoff"} {
			if _, ok := raw[k]; !ok {
				t.Fatalf("checkpoint detail %q lacks %s", d, k)
			}
		}
		if err := json.Unmarshal([]byte(d), &out[i]); err != nil {
			t.Fatalf("checkpoint detail %q has invalid field types: %v", d, err)
		}
	}
	return out
}

func prune(t *testing.T, s *SQLiteStore, cutoff time.Time) int {
	t.Helper()
	n, err := s.PruneBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PruneBefore: %v", err)
	}
	return n
}

func TestPruneBefore_WritesCheckpointAndVerifies(t *testing.T) {
	s := newChainStore(t)
	insertAt(t, s, eventAt(pruneOld, "old 1"), eventAt(pruneOld.Add(time.Second), "old 2"), eventAt(time.Now().UTC(), "recent"))
	_, hash2 := storedHash(t, s, 2)

	if n := prune(t, s, pruneCutoff); n != 2 {
		t.Fatalf("pruned %d, want 2", n)
	}
	cps := checkpoints(t, s)
	if len(cps) != 1 {
		t.Fatalf("%d checkpoints, want 1", len(cps))
	}
	cp := cps[0]
	if cp.PrunedRows != 2 || cp.PrunedThroughID != 2 || cp.PrunedThroughHash != hash2 || cp.Cutoff != pruneCutoff.Format(time.RFC3339Nano) {
		t.Fatalf("checkpoint = %+v", cp)
	}
	var status, source, summary string
	if err := s.db.QueryRow(`SELECT status, source, summary FROM audit_events WHERE action = ?`, ActionPruneCheckpoint).Scan(&status, &source, &summary); err != nil {
		t.Fatal(err)
	}
	if status != StatusOK || source != "retention" || summary != "Retention pruned 2 events up to id 2" {
		t.Fatalf("checkpoint row: %s %s %q", status, source, summary)
	}

	rep := verifyChain(t, s)
	requireIntact(t, rep)
	if rep.Start != ChainStartCheckpoint || rep.CheckpointID != 4 || rep.PrunedThroughID != 2 || rep.FirstID != 3 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestPruneBefore_NothingOldWritesNothing(t *testing.T) {
	s := newChainStore(t)
	insertAt(t, s, eventAt(time.Now().UTC(), "recent"))
	if n := prune(t, s, pruneCutoff); n != 0 {
		t.Fatalf("pruned %d, want 0", n)
	}
	if cps := checkpoints(t, s); len(cps) != 0 {
		t.Fatalf("%d checkpoints, want 0", len(cps))
	}
	if n := prune(t, newChainStore(t), pruneCutoff); n != 0 {
		t.Fatalf("empty store pruned %d", n)
	}
}

func TestPruneBefore_EverythingOldLeavesCheckpointOnly(t *testing.T) {
	s := newChainStore(t)
	insertAt(t, s, eventAt(pruneOld, "old 1"), eventAt(pruneOld.Add(time.Second), "old 2"))
	if n := prune(t, s, pruneCutoff); n != 2 {
		t.Fatalf("pruned %d, want 2", n)
	}
	_, total, _ := s.List(context.Background(), ListOpts{})
	if total != 1 {
		t.Fatalf("%d rows left, want only the checkpoint", total)
	}
	prev, cpHash := storedHash(t, s, 3)
	if cp := checkpoints(t, s)[0]; prev != cp.PrunedThroughHash {
		t.Fatalf("checkpoint prev_hash %q != pruned_through_hash %q", prev, cp.PrunedThroughHash)
	}
	requireIntact(t, verifyChain(t, s))

	insertAt(t, s, eventAt(time.Now().UTC(), "later"))
	if prev4, _ := storedHash(t, s, 4); prev4 != cpHash {
		t.Fatalf("later row prev_hash %q, want checkpoint hash %q", prev4, cpHash)
	}
	requireIntact(t, verifyChain(t, s))
}

func TestPruneBefore_SecondPruneRemovesFirstCheckpoint(t *testing.T) {
	s := newChainStore(t)
	insertAt(t, s, eventAt(pruneOld, "old"), eventAt(pruneCutoff.Add(time.Hour), "middle"))
	prune(t, s, pruneCutoff) // deletes 1, checkpoint 3
	insertAt(t, s, eventAt(time.Now().UTC().Add(2*time.Hour), "recent"))
	// The first checkpoint is stamped now, so a cutoff an hour ahead removes
	// ids 2 and 3 and keeps 4.
	prune(t, s, time.Now().UTC().Add(time.Hour))

	if cps := checkpoints(t, s); len(cps) != 1 {
		t.Fatalf("%d checkpoints, want 1", len(cps))
	}
	rep := verifyChain(t, s)
	requireIntact(t, rep)
	if rep.Start != ChainStartCheckpoint {
		t.Fatalf("start = %s", rep.Start)
	}
}

func TestPruneBefore_DeletesOnlyAnIDPrefix(t *testing.T) {
	s := newChainStore(t)
	insertAt(t, s, eventAt(pruneOld, "old 1"), eventAt(time.Now().UTC(), "recent"), eventAt(pruneOld, "old 3"))
	if n := prune(t, s, pruneCutoff); n != 1 {
		t.Fatalf("pruned %d, want only id 1", n)
	}
	var ids []int64
	if err := s.db.Select(&ids, `SELECT id FROM audit_events ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 2 || ids[1] != 3 {
		t.Fatalf("ids left = %v, want 2, 3 and the checkpoint", ids)
	}
	requireIntact(t, verifyChain(t, s))
}

func TestPruneBefore_LegacyRowsOnly(t *testing.T) {
	s := newChainStore(t)
	rawExec(t, s, `INSERT INTO audit_events (timestamp, category, action, summary) VALUES (?, 'tool_call', 'execute', 'legacy')`,
		pruneOld.Format(time.RFC3339Nano))
	insertAt(t, s, eventAt(time.Now().UTC(), "recent"))
	if n := prune(t, s, pruneCutoff); n != 1 {
		t.Fatalf("pruned %d, want 1", n)
	}
	if cp := checkpoints(t, s)[0]; cp.PrunedThroughHash != "" {
		t.Fatalf("pruned_through_hash = %q, want empty", cp.PrunedThroughHash)
	}
	requireIntact(t, verifyChain(t, s))
}

// --- concurrency and migration (file stores) ---

func TestChain_ConcurrentInsertAndPrune(t *testing.T) {
	s := newChainFileStore(t, filepath.Join(t.TempDir(), "audit.db"))
	ctx := context.Background()
	start := time.Now().UTC()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 30 {
			batch := []Event{eventAt(start.Add(time.Duration(i)*time.Millisecond), "a"), eventAt(start, "b")}
			if err := s.InsertBatch(ctx, batch); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 30 {
			if _, err := s.PruneBefore(ctx, start.Add(time.Duration(i)*time.Millisecond)); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	requireIntact(t, verifyChain(t, s))
}

func TestChain_TwoHandlesOnOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	a := newChainFileStore(t, path)
	b := newChainFileStore(t, path)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, s := range []*SQLiteStore{a, b} {
		wg.Add(1)
		go func(s *SQLiteStore) {
			defer wg.Done()
			for i := range 25 {
				if err := s.InsertBatch(context.Background(), []Event{chainEvent(i), chainEvent(i + 1)}); err != nil {
					errs <- err
					return
				}
			}
		}(s)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	rep := verifyChain(t, a)
	requireIntact(t, rep)
	if rep.Verified != 100 {
		t.Fatalf("verified %d, want 100", rep.Verified)
	}
}

// oldSchema is audit_events as it was before the hash columns.
const oldSchema = `CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL,
    category TEXT NOT NULL,
    action TEXT NOT NULL,
    agent TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ok',
    duration_ms INTEGER DEFAULT 0,
    source TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT ''
)`

const oldInsert = `INSERT INTO audit_events (timestamp, category, action, summary) VALUES (?, 'tool_call', 'execute', 'old')`

// createOldSchemaFile writes an audit.db with the old table and two rows, and
// returns a raw connection to it that the caller may keep open.
func createOldSchemaFile(t *testing.T) (string, *sqlx.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.db")
	raw, err := sqlx.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := raw.Exec(oldInsert, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	return path, raw
}

func TestMigration_OldSchemaFileOpens(t *testing.T) {
	path, raw := createOldSchemaFile(t)
	_ = raw.Close()

	s := newChainFileStore(t, path)
	_, total, err := s.List(context.Background(), ListOpts{})
	if err != nil || total != 2 {
		t.Fatalf("List: total %d, err %v", total, err)
	}
	rep := verifyChain(t, s)
	requireIntact(t, rep)
	if rep.Start != ChainStartNone || rep.LegacyRows != 2 {
		t.Fatalf("report = %+v", rep)
	}
	_ = s.Close()
	again, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	_ = again.Close()
}

func TestMigration_LiveOldWriterKeepsWorking(t *testing.T) {
	path, raw := createOldSchemaFile(t)
	s := newChainFileStore(t, path)
	if _, err := raw.Exec(oldInsert, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("old-style insert after migration: %v", err)
	}
	_, total, _ := s.List(context.Background(), ListOpts{})
	if total != 3 {
		t.Fatalf("total %d, want 3", total)
	}
}

func TestOpenSQLiteStoreReadOnly_MissingFile(t *testing.T) {
	if _, err := OpenSQLiteStoreReadOnly(filepath.Join(t.TempDir(), "nope.db")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestOpenSQLiteStoreReadOnly_RefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	newChainFileStore(t, path)
	ro, err := OpenSQLiteStoreReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close() //nolint:errcheck
	if err := ro.InsertBatch(context.Background(), []Event{chainEvent(1)}); err == nil {
		t.Fatal("insert through a read-only store succeeded")
	}
}

func TestOpenSQLiteStoreReadOnly_VerifiesWhileWriterOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	w := newChainFileStore(t, path)
	insertChained(t, w, 1, 3)
	ro, err := OpenSQLiteStoreReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close() //nolint:errcheck
	insertChained(t, w, 4, 2)
	rep := verifyChain(t, ro)
	requireIntact(t, rep)
	if rep.Verified != 5 {
		t.Fatalf("verified %d, want 5", rep.Verified)
	}
}

func TestOpenSQLiteStoreReadOnly_OldSchemaIsAllLegacy(t *testing.T) {
	path, raw := createOldSchemaFile(t)
	_ = raw.Close()
	ro, err := OpenSQLiteStoreReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close() //nolint:errcheck
	rep := verifyChain(t, ro)
	requireIntact(t, rep)
	if rep.Start != ChainStartNone || rep.LegacyRows != 2 || rep.Rows != 2 {
		t.Fatalf("report = %+v", rep)
	}
}
