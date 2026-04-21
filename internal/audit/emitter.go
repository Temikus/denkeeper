package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// BufferedEmitter accepts events via a channel and writes them in batches.
type BufferedEmitter struct {
	store  Store
	ch     chan Event
	logger *slog.Logger
	wg     sync.WaitGroup
	done   chan struct{}
}

// NewBufferedEmitter creates a buffered emitter with the given buffer capacity.
func NewBufferedEmitter(store Store, bufSize int, logger *slog.Logger) *BufferedEmitter {
	if bufSize <= 0 {
		bufSize = 1000
	}
	return &BufferedEmitter{
		store:  store,
		ch:     make(chan Event, bufSize),
		logger: logger,
		done:   make(chan struct{}),
	}
}

// Emit queues an event for persistence. Non-blocking; drops events if buffer is full.
func (e *BufferedEmitter) Emit(_ context.Context, event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.Status == "" {
		event.Status = StatusOK
	}

	select {
	case e.ch <- event:
	default:
		e.logger.Warn("audit buffer full, dropping event",
			"category", event.Category, "action", event.Action)
	}
}

// Start begins the background flush loop. Call Close to stop.
func (e *BufferedEmitter) Start(ctx context.Context) {
	e.wg.Add(1)
	go e.flushLoop(ctx) //nolint:gosec // background goroutine intentionally outlives request contexts
}

func (e *BufferedEmitter) flushLoop(ctx context.Context) {
	defer e.wg.Done()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	batch := make([]Event, 0, 20)

	for {
		select {
		case ev, ok := <-e.ch:
			if !ok {
				// Channel closed — flush remaining.
				if len(batch) > 0 {
					e.flush(ctx, batch)
				}
				return
			}
			batch = append(batch, ev)
			if len(batch) >= 20 {
				e.flush(ctx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				e.flush(ctx, batch)
				batch = batch[:0]
			}

		case <-ctx.Done():
			// Drain remaining events from the channel.
		drain:
			for {
				select {
				case ev, ok := <-e.ch:
					if !ok {
						break drain
					}
					batch = append(batch, ev)
				default:
					break drain
				}
			}
			if len(batch) > 0 {
				e.flush(context.Background(), batch)
			}
			return
		}
	}
}

func (e *BufferedEmitter) flush(ctx context.Context, batch []Event) {
	if err := e.store.InsertBatch(ctx, batch); err != nil {
		e.logger.Error("failed to flush audit events", "count", len(batch), "error", err)
	}
}

// Close stops the flush loop and drains remaining events.
func (e *BufferedEmitter) Close() {
	close(e.ch)
	e.wg.Wait()
}

// NopEmitter discards all events.
type NopEmitter struct{}

// Emit is a no-op.
func (NopEmitter) Emit(context.Context, Event) {}
