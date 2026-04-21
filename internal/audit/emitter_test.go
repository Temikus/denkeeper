package audit

import (
	"context"
	"log/slog"
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

func TestNopEmitter(t *testing.T) {
	var e NopEmitter
	e.Emit(context.Background(), Event{
		Category: CategoryToolCall,
		Action:   "execute",
		Summary:  "should be dropped",
	})
	// No panic = pass.
}
