package audit

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gateStore blocks InsertBatch until released, simulating a wedged SQLite
// writer while a burst arrives. Only the emitter's InsertBatch path is
// exercised; the rest of the Store interface is inert.
type gateStore struct {
	release  chan struct{}
	inserted atomic.Int64
}

func newGateStore() *gateStore {
	return &gateStore{release: make(chan struct{})}
}

func (s *gateStore) InsertBatch(_ context.Context, events []Event) error {
	<-s.release
	s.inserted.Add(int64(len(events)))
	return nil
}

func (s *gateStore) Insert(_ context.Context, _ Event) error { return nil }

func (s *gateStore) List(_ context.Context, _ ListOpts) ([]Event, int, error) {
	return nil, 0, nil
}

func (s *gateStore) Stats(_ context.Context, _ StatsOpts) (*Stats, error) {
	return &Stats{}, nil
}

func (s *gateStore) PruneBefore(_ context.Context, _ time.Time) (int, error) { return 0, nil }

func (s *gateStore) Close() error { return nil }

// dropCounter counts the "audit buffer full" warning Emit logs per dropped
// event — the only externally observable drop signal the emitter has.
type dropCounter struct {
	mu    sync.Mutex
	drops int
}

func (h *dropCounter) Enabled(context.Context, slog.Level) bool { return true }

func (h *dropCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "audit buffer full, dropping event" {
		h.mu.Lock()
		h.drops++
		h.mu.Unlock()
	}
	return nil
}

func (h *dropCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *dropCounter) WithGroup(string) slog.Handler      { return h }

func (h *dropCounter) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.drops
}

// A 2000-event burst against the default-size buffer with the store wedged:
// Emit must never block the producer, every overflow event must be dropped
// with a warning, and delivered+dropped must account for the whole burst.
// Capacity while the store is stuck is bufSize + one in-flight batch (20).
func TestBufferedEmitter_OverflowDropsInsteadOfBlocking(t *testing.T) {
	store := newGateStore()
	drops := &dropCounter{}
	emitter := NewBufferedEmitter(store, 1000, slog.New(drops))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter.Start(ctx)

	const burst = 2000
	start := time.Now()
	for i := 0; i < burst; i++ {
		emitter.Emit(ctx, Event{
			Category: CategoryLLM,
			Action:   "complete",
			Summary:  "burst",
		})
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("Emit burst took %v — producer appears to block on a full buffer", elapsed)
	}

	close(store.release)
	emitter.Flush()
	emitter.Close()

	delivered := int(store.inserted.Load())
	dropped := drops.count()
	if delivered+dropped != burst {
		t.Fatalf("accounting mismatch: delivered=%d dropped=%d, want sum %d", delivered, dropped, burst)
	}
	if dropped == 0 {
		t.Fatal("expected drops when burst exceeds buffer with store wedged")
	}
	if delivered < 1000 || delivered > 1020 {
		t.Fatalf("delivered=%d, want bufSize..bufSize+batch (1000..1020)", delivered)
	}
	t.Logf("burst=%d buffer=1000 store wedged: delivered=%d dropped=%d, emit loop took %v", burst, delivered, dropped, elapsed)
}

// Two concurrent producers of 1000 events each ([eval] max_concurrent = 2):
// same invariants — no producer blocking, exact drop accounting.
func TestBufferedEmitter_ConcurrentProducerOverflowAccounting(t *testing.T) {
	store := newGateStore()
	drops := &dropCounter{}
	emitter := NewBufferedEmitter(store, 1000, slog.New(drops))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emitter.Start(ctx)

	const producers, perProducer = 2, 1000
	start := time.Now()
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				emitter.Emit(ctx, Event{
					Category: CategoryToolCall,
					Action:   "execute",
					Summary:  "concurrent burst",
				})
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("concurrent Emit burst took %v — producer appears to block on a full buffer", elapsed)
	}

	close(store.release)
	emitter.Flush()
	emitter.Close()

	total := producers * perProducer
	delivered := int(store.inserted.Load())
	dropped := drops.count()
	if delivered+dropped != total {
		t.Fatalf("accounting mismatch: delivered=%d dropped=%d, want sum %d", delivered, dropped, total)
	}
	if dropped == 0 {
		t.Fatal("expected drops when combined burst exceeds buffer with store wedged")
	}
	if delivered < 1000 || delivered > 1020 {
		t.Fatalf("delivered=%d, want bufSize..bufSize+batch (1000..1020)", delivered)
	}
	t.Logf("2x%d producers, buffer=1000, store wedged: delivered=%d dropped=%d, emit took %v", perProducer, delivered, dropped, elapsed)
}
