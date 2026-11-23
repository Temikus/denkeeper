package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BufferedEmitter accepts events via a channel and writes them in batches.
type BufferedEmitter struct {
	store    Store
	ch       chan Event
	bufSize  int
	logger   *slog.Logger
	wg       sync.WaitGroup
	done     chan struct{}
	flushReq chan chan struct{} // Flush() sends a signal; flushLoop acks when done

	// Events lost since the last dropped_events record: refused by a full
	// channel, or in a batch whose InsertBatch failed. firstLoss is the unix
	// nanos of the first of them, 0 when none.
	dropped     atomic.Int64
	writeFailed atomic.Int64
	firstLoss   atomic.Int64
}

// NewBufferedEmitter creates a buffered emitter with the given buffer capacity.
func NewBufferedEmitter(store Store, bufSize int, logger *slog.Logger) *BufferedEmitter {
	if bufSize <= 0 {
		bufSize = 1000
	}
	return &BufferedEmitter{
		store:    store,
		ch:       make(chan Event, bufSize),
		bufSize:  bufSize,
		logger:   logger,
		done:     make(chan struct{}),
		flushReq: make(chan chan struct{}),
	}
}

// Emit queues an event for persistence. Non-blocking; drops events if buffer is full.
// A dropped event is counted and recorded in a dropped_events row once the
// buffer drains.
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
		e.noteLoss(time.Now())
		e.dropped.Add(1)
		e.logger.Warn("audit buffer full, dropping event",
			"category", event.Category, "action", event.Action)
	}
}

// Start begins the background flush loop. Call Close to stop.
func (e *BufferedEmitter) Start(ctx context.Context) {
	e.wg.Add(1)
	go e.flushLoop(ctx) //nolint:gosec // background goroutine intentionally outlives request contexts
}

// flushLoop batches events. The loss record is added only where the channel
// is known to be empty (tick, Flush, shutdown), never on the 20-event path
// while a burst is still arriving, so a burst yields one record.
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
				e.flush(ctx, batch, true)
				return
			}
			batch = append(batch, ev)
			if len(batch) >= 20 {
				e.flush(ctx, batch, false)
				batch = batch[:0]
			}

		case <-ticker.C:
			e.flush(ctx, batch, len(e.ch) == 0)
			batch = batch[:0]

		case ack := <-e.flushReq:
			batch = e.drainChannel(batch)
			e.flush(ctx, batch, true)
			batch = batch[:0]
			close(ack)

		case <-ctx.Done():
			batch = e.drainChannel(batch)
			e.flush(context.Background(), batch, true)
			return
		}
	}
}

// drainChannel reads all immediately available events from the channel
// into batch without blocking.
func (e *BufferedEmitter) drainChannel(batch []Event) []Event {
	for {
		select {
		case ev, ok := <-e.ch:
			if !ok {
				return batch
			}
			batch = append(batch, ev)
		default:
			return batch
		}
	}
}

// flush writes batch, plus the loss record when drained is true and there
// are losses to report. On failure the batch's own events are counted as
// lost and a taken record's counts are put back.
func (e *BufferedEmitter) flush(ctx context.Context, batch []Event, drained bool) {
	var taken *lossCounts
	if drained {
		if c, ok := e.takeLoss(); ok {
			taken = &c
			batch = append(batch, e.lossRecord(c))
		}
	}
	if len(batch) == 0 {
		return
	}
	err := e.store.InsertBatch(ctx, batch)
	if err == nil {
		return
	}
	own := len(batch)
	if taken != nil {
		own--
		e.restoreLoss(*taken)
	}
	e.noteLoss(time.Now())
	e.writeFailed.Add(int64(own))
	e.logger.Error("failed to flush audit events", "count", own, "error", err)
}

type lossCounts struct {
	dropped, writeFailed int64
	since                time.Time
}

// lossDetail is the detail of a dropped_events record.
type lossDetail struct {
	Dropped     int64  `json:"dropped"`
	WriteFailed int64  `json:"write_failed"`
	BufferSize  int    `json:"buffer_size"`
	Since       string `json:"since"`
	Until       string `json:"until"`
}

func (e *BufferedEmitter) noteLoss(at time.Time) {
	e.firstLoss.CompareAndSwap(0, at.UnixNano())
}

func (e *BufferedEmitter) takeLoss() (lossCounts, bool) {
	c := lossCounts{dropped: e.dropped.Swap(0), writeFailed: e.writeFailed.Swap(0)}
	since := e.firstLoss.Swap(0)
	if c.dropped == 0 && c.writeFailed == 0 {
		if since != 0 {
			e.firstLoss.CompareAndSwap(0, since)
		}
		return c, false
	}
	if since != 0 {
		c.since = time.Unix(0, since).UTC()
	}
	return c, true
}

// restoreLoss puts taken counts back, keeping the earliest first-loss time.
func (e *BufferedEmitter) restoreLoss(c lossCounts) {
	e.dropped.Add(c.dropped)
	e.writeFailed.Add(c.writeFailed)
	if c.since.IsZero() {
		return
	}
	since := c.since.UnixNano()
	for {
		cur := e.firstLoss.Load()
		if cur != 0 && cur <= since {
			return
		}
		if e.firstLoss.CompareAndSwap(cur, since) {
			return
		}
	}
}

func (e *BufferedEmitter) lossRecord(c lossCounts) Event {
	now := time.Now().UTC()
	since := c.since
	if since.IsZero() || since.After(now) {
		since = now
	}
	var parts []string
	if c.dropped > 0 {
		parts = append(parts, fmt.Sprintf("%d dropped (buffer full)", c.dropped))
	}
	if c.writeFailed > 0 {
		parts = append(parts, fmt.Sprintf("%d not written (store error)", c.writeFailed))
	}
	detail, _ := json.Marshal(lossDetail{
		Dropped: c.dropped, WriteFailed: c.writeFailed, BufferSize: e.bufSize,
		Since: since.Format(time.RFC3339Nano), Until: now.Format(time.RFC3339Nano),
	})
	return Event{
		Timestamp: now,
		Category:  CategoryAudit,
		Action:    ActionDroppedEvents,
		Summary:   fmt.Sprintf("Audit log lost %d events: %s", c.dropped+c.writeFailed, strings.Join(parts, ", ")),
		Detail:    string(detail),
		Status:    StatusError,
		Source:    "emitter",
	}
}

// Flush synchronously drains all buffered events and writes them to the store.
// The emitter remains usable after Flush returns. Safe for concurrent use.
// Guarantees: all events emitted before Flush is called will be persisted
// when Flush returns. Events emitted concurrently during Flush may or may
// not be included.
func (e *BufferedEmitter) Flush() {
	ack := make(chan struct{})
	e.flushReq <- ack
	<-ack
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
