package mcpserver

import (
	"context"
	"errors"
	"sync"
	"time"
)

// errChatIdleTimeout is the cancellation cause when a chat turn produces no
// progress events for the configured timeout. Detect via
// errors.Is(context.Cause(ctx), errChatIdleTimeout).
var errChatIdleTimeout = errors.New("chat idle timeout: no progress events")

// idleWatchdog cancels a context when Touch is not called within timeout.
// Unlike a fixed deadline, it bounds stalls rather than total work: a chat
// turn that keeps emitting events (tool rounds, streamed content) can run
// indefinitely, while one that goes quiet is cancelled. Mirrors the
// llm.IdleTimeoutReader pattern.
type idleWatchdog struct {
	timeout time.Duration
	timer   *time.Timer
	cancel  context.CancelCauseFunc
	once    sync.Once
}

// newIdleWatchdog returns a child context that is cancelled with
// errChatIdleTimeout if Touch is not called within timeout. The caller must
// defer Stop to release the timer and cancel the context on normal return.
func newIdleWatchdog(ctx context.Context, timeout time.Duration) (context.Context, *idleWatchdog) {
	ctx, cancel := context.WithCancelCause(ctx)
	w := &idleWatchdog{timeout: timeout, cancel: cancel}
	w.timer = time.AfterFunc(timeout, func() {
		w.cancel(errChatIdleTimeout)
	})
	return ctx, w
}

// Touch resets the idle timer. Safe to call concurrently with the timer
// firing: cancellation is idempotent, and a fire racing a touch means the
// turn dies at the boundary, which is acceptable.
func (w *idleWatchdog) Touch() {
	w.timer.Reset(w.timeout)
}

// Stop halts the timer and cancels the context without the timeout cause.
// Idempotent.
func (w *idleWatchdog) Stop() {
	w.once.Do(func() {
		w.timer.Stop()
		w.cancel(nil)
	})
}
