package llm

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// ErrStreamIdleTimeout is returned when the LLM SSE stream has not produced
// any data for longer than the configured idle timeout.
var ErrStreamIdleTimeout = errors.New("LLM stream idle timeout: no data received")

// IdleTimeoutReader wraps an io.Reader and cancels the associated context if
// no data is read within the configured timeout. Each successful Read that
// returns n > 0 resets the timer.
//
// This is designed for LLM SSE streaming responses where we want to detect
// stalled connections without setting a fixed deadline on the entire request
// (which would kill legitimately long-running streams that are actively
// producing data). It is NOT used for MCP tool SSE connections — those use
// per-request context deadlines and TCP keepalive instead (see 087357b).
type IdleTimeoutReader struct {
	reader  io.Reader
	timeout time.Duration
	cancel  context.CancelCauseFunc
	timer   *time.Timer
	once    sync.Once
}

// NewIdleTimeoutReader creates a reader that cancels the given context if no
// data arrives within timeout. The caller must call Stop() when done (typically
// via defer) to release the timer.
func NewIdleTimeoutReader(r io.Reader, timeout time.Duration, cancel context.CancelCauseFunc) *IdleTimeoutReader {
	itr := &IdleTimeoutReader{
		reader:  r,
		timeout: timeout,
		cancel:  cancel,
	}
	itr.timer = time.AfterFunc(timeout, func() {
		itr.cancel(ErrStreamIdleTimeout)
	})
	return itr
}

// Read delegates to the underlying reader and resets the idle timer whenever
// data is successfully received.
func (r *IdleTimeoutReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.timer.Reset(r.timeout)
	}
	return n, err
}

// Stop releases the idle timer. Safe to call multiple times.
func (r *IdleTimeoutReader) Stop() {
	r.once.Do(func() {
		r.timer.Stop()
	})
}

// StreamIdleConfig bundles the parameters needed to guard an SSE stream
// against stalled providers. Pass nil to ReadOAIStream to disable.
type StreamIdleConfig struct {
	Ctx     context.Context         // the context created with WithCancelCause
	Timeout time.Duration           // idle timeout duration
	Cancel  context.CancelCauseFunc // cancels Ctx when idle timeout fires
}

// StreamIdleConfigFor returns a *StreamIdleConfig if timeout > 0, or nil
// otherwise. This is a convenience for providers that conditionally enable
// idle timeout based on the ChatRequest.
func StreamIdleConfigFor(ctx context.Context, timeout time.Duration, cancel context.CancelCauseFunc) *StreamIdleConfig {
	if timeout <= 0 {
		return nil
	}
	return &StreamIdleConfig{Ctx: ctx, Timeout: timeout, Cancel: cancel}
}
