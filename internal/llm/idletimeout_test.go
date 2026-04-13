package llm

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestIdleTimeoutReader_NormalRead(t *testing.T) {
	// Reader that returns data immediately — timer should never fire.
	r := strings.NewReader("hello world")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	itr := NewIdleTimeoutReader(r, 1*time.Second, cancel)
	defer itr.Stop()

	buf := make([]byte, 64)
	n, err := itr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(buf[:n]); got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}

	// Context should still be active.
	if ctx.Err() != nil {
		t.Fatal("context should not be cancelled after normal read")
	}
}

func TestIdleTimeoutReader_IdleTimeout(t *testing.T) {
	// Use a pipe where the writer never sends data. Close the read end
	// when the context is cancelled to simulate how http.Response.Body
	// behaves on context cancellation.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	// Simulate HTTP transport behaviour: when context is cancelled, close
	// the reader so the blocked Read returns.
	go func() {
		<-ctx.Done()
		pr.CloseWithError(context.Cause(ctx))
	}()

	itr := NewIdleTimeoutReader(pr, 50*time.Millisecond, cancel)
	defer itr.Stop()

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, err := itr.Read(buf)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from idle timeout, got nil")
		}
		// The cause should be ErrStreamIdleTimeout.
		if cause := context.Cause(ctx); !errors.Is(cause, ErrStreamIdleTimeout) {
			t.Fatalf("expected ErrStreamIdleTimeout cause, got: %v", cause)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for idle timeout to fire")
	}
}

func TestIdleTimeoutReader_ResetOnData(t *testing.T) {
	// Simulate a slow stream: data arrives in chunks with pauses shorter
	// than the idle timeout.
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	itr := NewIdleTimeoutReader(pr, 100*time.Millisecond, cancel)
	defer itr.Stop()

	// Write 3 chunks with 50ms gaps (well under the 100ms timeout).
	go func() {
		for _, chunk := range []string{"aaa", "bbb", "ccc"} {
			time.Sleep(50 * time.Millisecond)
			_, _ = pw.Write([]byte(chunk))
		}
		_ = pw.Close()
	}()

	var got strings.Builder
	buf := make([]byte, 64)
	for {
		n, err := itr.Read(buf)
		if n > 0 {
			got.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if got.String() != "aaabbbccc" {
		t.Fatalf("got %q, want %q", got.String(), "aaabbbccc")
	}
	if ctx.Err() != nil {
		t.Fatal("context should not be cancelled when data arrives regularly")
	}
}

func TestIdleTimeoutReader_StopPreventsTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	defer func() { _ = pr.Close() }()

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	itr := NewIdleTimeoutReader(pr, 50*time.Millisecond, cancel)

	// Stop before the timeout fires.
	itr.Stop()
	time.Sleep(100 * time.Millisecond)

	if ctx.Err() != nil {
		t.Fatal("context should not be cancelled after Stop()")
	}
}
