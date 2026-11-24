package audit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBufferedEmitter_FlushOnClose(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	logger := slog.Default()
	emitter := NewBufferedEmitter(store, 100, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter.Start(ctx)

	// Emit a few events.
	for i := 0; i < 5; i++ {
		emitter.Emit(ctx, Event{
			Category: CategoryToolCall,
			Action:   "execute",
			Summary:  "test event",
		})
	}

	// Close should drain.
	emitter.Close()

	events, total, err := store.List(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected 5 events after close, got %d", total)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
}

func TestBufferedEmitter_FlushOnBatchSize(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	logger := slog.Default()
	emitter := NewBufferedEmitter(store, 100, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter.Start(ctx)

	// Emit 20 events (batch threshold).
	for i := 0; i < 20; i++ {
		emitter.Emit(ctx, Event{
			Category: CategoryLLM,
			Action:   "complete",
			Summary:  "batch test",
		})
	}

	// Wait for flush.
	time.Sleep(100 * time.Millisecond)

	_, total, err := store.List(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 20 {
		t.Fatalf("expected 20 events after batch flush, got %d", total)
	}

	emitter.Close()
}

func TestBufferedEmitter_DefaultsTimestampAndStatus(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	logger := slog.Default()
	emitter := NewBufferedEmitter(store, 100, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter.Start(ctx)

	emitter.Emit(ctx, Event{
		Category: CategoryConfig,
		Action:   "update",
		Summary:  "updated persona",
	})

	emitter.Close()

	events, _, _ := store.List(context.Background(), ListOpts{})
	if len(events) != 1 {
		t.Fatal("expected 1 event")
	}
	if events[0].Status != StatusOK {
		t.Errorf("expected status %q, got %q", StatusOK, events[0].Status)
	}
	if events[0].Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestBufferedEmitter_Flush(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	logger := slog.Default()
	emitter := NewBufferedEmitter(store, 100, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter.Start(ctx)

	// Emit events.
	for i := 0; i < 10; i++ {
		emitter.Emit(ctx, Event{
			Category: CategorySchedule,
			Action:   "fire",
			Summary:  "flush test",
		})
	}

	// Flush should persist all events synchronously.
	emitter.Flush()

	events, total, err := store.List(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 10 {
		t.Fatalf("expected 10 events after Flush, got %d", total)
	}
	if len(events) != 10 {
		t.Fatalf("expected 10 events, got %d", len(events))
	}

	// Emitter should still be usable after Flush.
	emitter.Emit(ctx, Event{
		Category: CategorySchedule,
		Action:   "fire",
		Summary:  "post-flush",
	})
	emitter.Flush()

	_, total, err = store.List(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 11 {
		t.Fatalf("expected 11 events after second Flush, got %d", total)
	}

	emitter.Close()
}

func TestBufferedEmitter_FlushConcurrent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	logger := slog.Default()
	emitter := NewBufferedEmitter(store, 1000, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter.Start(ctx)

	// Concurrent emits and flushes — should not race or panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			emitter.Emit(ctx, Event{
				Category: CategoryToolCall,
				Action:   "execute",
				Summary:  "concurrent",
			})
		}
	}()

	// Flush multiple times while emits are in flight.
	for i := 0; i < 5; i++ {
		emitter.Flush()
	}
	<-done

	// Final flush to capture any stragglers.
	emitter.Flush()

	_, total, err := store.List(context.Background(), ListOpts{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 100 {
		t.Fatalf("expected 100 events after concurrent emit+flush, got %d", total)
	}

	emitter.Close()
}

func TestNopEmitter(t *testing.T) {
	var e NopEmitter
	e.Emit(context.Background(), Event{
		Category: CategoryToolCall,
		Action:   "execute",
		Summary:  "should be dropped",
	})
	// No panic = pass.
}

// --- counted losses ---

func lossStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func emitN(e *BufferedEmitter, n int) {
	for range n {
		e.Emit(context.Background(), Event{Category: CategoryToolCall, Action: "execute", Summary: "e"})
	}
}

// lossRecords returns the dropped_events rows and the count of other rows.
func lossRecords(t *testing.T, s *SQLiteStore) ([]Event, []lossDetail, int) {
	t.Helper()
	events, _, err := s.List(context.Background(), ListOpts{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	var recs []Event
	var details []lossDetail
	others := 0
	for _, ev := range events {
		if ev.Action != ActionDroppedEvents {
			others++
			continue
		}
		var d lossDetail
		if err := json.Unmarshal([]byte(ev.Detail), &d); err != nil {
			t.Fatalf("record detail %q: %v", ev.Detail, err)
		}
		recs = append(recs, ev)
		details = append(details, d)
	}
	return recs, details, others
}

// startedWithDrops fills a buffer of 5 with 12 events before Start, so 7 drop.
func startedWithDrops(t *testing.T, store Store) *BufferedEmitter {
	t.Helper()
	e := NewBufferedEmitter(store, 5, slog.New(slog.NewTextHandler(io.Discard, nil)))
	emitN(e, 12)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.Start(ctx)
	return e
}

func TestBufferedEmitter_RecordsDropsOnceDrained(t *testing.T) {
	store := lossStore(t)
	e := startedWithDrops(t, store)
	e.Flush()

	recs, details, others := lossRecords(t, store)
	if others != 5 || len(recs) != 1 {
		t.Fatalf("got %d events and %d records, want 5 and 1", others, len(recs))
	}
	r, d := recs[0], details[0]
	if r.Category != CategoryAudit || r.Status != StatusError || r.Source != "emitter" {
		t.Fatalf("record = %+v", r)
	}
	if d.Dropped != 7 || d.WriteFailed != 0 || d.BufferSize != 5 {
		t.Fatalf("detail = %+v", d)
	}
	if r.Summary != "Audit log lost 7 events: 7 dropped (buffer full)" {
		t.Fatalf("summary = %q", r.Summary)
	}
	e.Close()
}

func TestBufferedEmitter_NoDropsNoRecord(t *testing.T) {
	store := lossStore(t)
	e := NewBufferedEmitter(store, 5, slog.Default())
	e.Start(context.Background())
	emitN(e, 3)
	e.Flush()
	e.Close()
	if recs, _, others := lossRecords(t, store); len(recs) != 0 || others != 3 {
		t.Fatalf("got %d records, %d events", len(recs), others)
	}
}

// holdStore lets a test park the flush goroutine inside InsertBatch, so a
// burst overflows the channel deterministically.
type holdStore struct {
	*SQLiteStore
	mu      sync.Mutex
	hold    chan struct{} // nil: pass straight through
	entered chan struct{}
}

func (s *holdStore) InsertBatch(ctx context.Context, events []Event) error {
	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()
	if hold != nil {
		s.entered <- struct{}{}
		<-hold
	}
	return s.SQLiteStore.InsertBatch(ctx, events)
}

func (s *holdStore) park() chan struct{} {
	release := make(chan struct{})
	s.mu.Lock()
	s.hold = release
	s.mu.Unlock()
	return release
}

func (s *holdStore) unpark(release chan struct{}) {
	s.mu.Lock()
	s.hold = nil
	s.mu.Unlock()
	close(release)
}

func TestBufferedEmitter_OneRecordPerDrain(t *testing.T) {
	store := &holdStore{SQLiteStore: lossStore(t), entered: make(chan struct{}, 1)}
	e := startedWithDrops(t, store)
	e.Flush()
	e.Flush()
	if recs, _, _ := lossRecords(t, store.SQLiteStore); len(recs) != 1 {
		t.Fatalf("%d records after two flushes, want 1", len(recs))
	}

	// Park the flush goroutine in InsertBatch, then overflow the buffer again.
	release := store.park()
	emitN(e, 1)
	<-store.entered // the 500ms tick flushed that event and is now parked
	emitN(e, 9)     // 5 fit, 4 drop
	store.unpark(release)
	e.Flush()

	_, details, _ := lossRecords(t, store.SQLiteStore)
	if len(details) != 2 {
		t.Fatalf("%d records, want 2", len(details))
	}
	if details[0].Dropped+details[1].Dropped != 11 || (details[0].Dropped != 4 && details[1].Dropped != 4) {
		t.Fatalf("details = %+v, want one with 7 and one with 4", details)
	}
	e.Close()
}

func TestBufferedEmitter_RecordOnCloseWithoutFlush(t *testing.T) {
	store := lossStore(t)
	e := startedWithDrops(t, store)
	e.Close()
	if recs, _, _ := lossRecords(t, store); len(recs) != 1 {
		t.Fatalf("%d records after Close, want 1", len(recs))
	}
}

// failStore fails the next n InsertBatch calls, then passes through.
type failStore struct {
	*SQLiteStore
	fails atomic.Int32
}

func (s *failStore) InsertBatch(ctx context.Context, events []Event) error {
	if s.fails.Add(-1) >= 0 {
		return errors.New("disk full")
	}
	return s.SQLiteStore.InsertBatch(ctx, events)
}

func newFailStore(t *testing.T, n int32) *failStore {
	s := &failStore{SQLiteStore: lossStore(t)}
	s.fails.Store(n)
	return s
}

func sumLoss(details []lossDetail) (dropped, failed int64) {
	for _, d := range details {
		dropped += d.Dropped
		failed += d.WriteFailed
	}
	return dropped, failed
}

func TestBufferedEmitter_CountsFailedWrites(t *testing.T) {
	store := newFailStore(t, 1)
	e := NewBufferedEmitter(store, 100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.Start(context.Background())
	emitN(e, 3)
	e.Flush() // the batch of 3 fails
	e.Flush()
	e.Close()

	recs, details, others := lossRecords(t, store.SQLiteStore)
	if others != 0 || len(recs) != 1 {
		t.Fatalf("got %d events and %d records", others, len(recs))
	}
	if details[0].WriteFailed != 3 || details[0].Dropped != 0 {
		t.Fatalf("detail = %+v", details[0])
	}
	if recs[0].Summary != "Audit log lost 3 events: 3 not written (store error)" {
		t.Fatalf("summary = %q", recs[0].Summary)
	}
}

func TestBufferedEmitter_FailedRecordFlushKeepsCounts(t *testing.T) {
	store := newFailStore(t, 1)
	e := startedWithDrops(t, store)
	e.Flush() // 5 events plus the record: fails
	e.Flush()
	e.Close()

	recs, details, others := lossRecords(t, store.SQLiteStore)
	if others != 0 || len(recs) == 0 {
		t.Fatalf("got %d events and %d records", others, len(recs))
	}
	if dropped, failed := sumLoss(details); dropped != 7 || failed != 5 {
		t.Fatalf("records count %d dropped and %d failed, want 7 and 5", dropped, failed)
	}
	if !strings.Contains(recs[len(recs)-1].Summary, "dropped (buffer full), ") {
		t.Fatalf("summary = %q", recs[len(recs)-1].Summary)
	}
}

func TestBufferedEmitter_LossRecordIsChained(t *testing.T) {
	store := lossStore(t)
	e := startedWithDrops(t, store)
	e.Flush()
	e.Close()
	rep, err := store.VerifyChain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Break != nil || rep.Verified != 6 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestBufferedEmitter_LossRecordWindow(t *testing.T) {
	store := lossStore(t)
	e := startedWithDrops(t, store)
	e.Flush()
	e.Close()
	_, details, _ := lossRecords(t, store)
	since, err1 := time.Parse(time.RFC3339Nano, details[0].Since)
	until, err2 := time.Parse(time.RFC3339Nano, details[0].Until)
	if err1 != nil || err2 != nil {
		t.Fatalf("since %q until %q", details[0].Since, details[0].Until)
	}
	if since.After(until) {
		t.Fatalf("since %v is after until %v", since, until)
	}
}
