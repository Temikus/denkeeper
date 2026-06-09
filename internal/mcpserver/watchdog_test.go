package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIdleWatchdog_FiresAfterTimeout(t *testing.T) {
	ctx, w := newIdleWatchdog(context.Background(), 20*time.Millisecond)
	defer w.Stop()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not fire")
	}
	if cause := context.Cause(ctx); !errors.Is(cause, errChatIdleTimeout) {
		t.Errorf("cause = %v, want errChatIdleTimeout", cause)
	}
}

func TestIdleWatchdog_TouchExtendsDeadline(t *testing.T) {
	ctx, w := newIdleWatchdog(context.Background(), 80*time.Millisecond)
	defer w.Stop()

	// Touch every 20ms for ~200ms — well past the 80ms timeout.
	for i := 0; i < 10; i++ {
		time.Sleep(20 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatalf("context cancelled at iteration %d despite touches: %v", i, context.Cause(ctx))
		}
		w.Touch()
	}

	// Stop touching: the watchdog must now fire.
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("watchdog did not fire after touches stopped")
	}
	if cause := context.Cause(ctx); !errors.Is(cause, errChatIdleTimeout) {
		t.Errorf("cause = %v, want errChatIdleTimeout", cause)
	}
}

func TestIdleWatchdog_StopPreventsTimeout(t *testing.T) {
	ctx, w := newIdleWatchdog(context.Background(), 20*time.Millisecond)
	w.Stop()

	time.Sleep(50 * time.Millisecond)
	if cause := context.Cause(ctx); errors.Is(cause, errChatIdleTimeout) {
		t.Errorf("cause = errChatIdleTimeout, want plain cancellation from Stop")
	}
	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		t.Errorf("cause = %v, want context.Canceled", cause)
	}
}

func TestIdleWatchdog_StopIdempotent(t *testing.T) {
	_, w := newIdleWatchdog(context.Background(), 20*time.Millisecond)
	w.Stop()
	w.Stop() // must not panic
}

func TestIdleWatchdog_ParentCancelWins(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	ctx, w := newIdleWatchdog(parent, time.Minute)
	defer w.Stop()

	parentCancel()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("child context not cancelled with parent")
	}
	if cause := context.Cause(ctx); errors.Is(cause, errChatIdleTimeout) {
		t.Errorf("cause = errChatIdleTimeout, want parent's cancellation")
	}
}

func TestChatErrorMessage_IdleTimeout(t *testing.T) {
	ctx, w := newIdleWatchdog(context.Background(), time.Millisecond)
	defer w.Stop()
	<-ctx.Done()

	msg := chatErrorMessage(ctx, fmt.Errorf("LLM completion: %w", context.Canceled), 2*time.Minute)
	if !strings.Contains(msg, "no progress for 2m0s") {
		t.Errorf("message = %q, want idle-timeout phrasing", msg)
	}
	if !strings.Contains(msg, "chat_timeout") {
		t.Errorf("message = %q, want pointer to the chat_timeout config knob", msg)
	}
}

func TestChatErrorMessage_OtherError(t *testing.T) {
	ctx, w := newIdleWatchdog(context.Background(), time.Minute)
	defer w.Stop()

	msg := chatErrorMessage(ctx, errors.New("agent exploded"), 2*time.Minute)
	if msg != "chat failed: agent exploded" {
		t.Errorf("message = %q, want plain chat failed wrapper", msg)
	}
}
