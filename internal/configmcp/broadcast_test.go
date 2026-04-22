package configmcp_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/configmcp"
)

// broadcastMockEmitter collects emitted audit events for test assertions.
type broadcastMockEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

func (m *broadcastMockEmitter) Emit(_ context.Context, event audit.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func (m *broadcastMockEmitter) Events() []audit.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]audit.Event(nil), m.events...)
}

func TestEmitBroadcastFailure_PartialFailure(t *testing.T) {
	em := &broadcastMockEmitter{}
	configmcp.EmitBroadcastFailure(context.Background(), em, true, "daily-check", "@work", "chan:work", 1, 1, "connection refused")

	events := em.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	evt := events[0]
	if evt.Category != audit.CategorySchedule {
		t.Errorf("category = %q, want %q", evt.Category, audit.CategorySchedule)
	}
	if evt.Action != "broadcast_partial_failure" {
		t.Errorf("action = %q, want broadcast_partial_failure", evt.Action)
	}
	if evt.Status != audit.StatusError {
		t.Errorf("status = %q, want %q", evt.Status, audit.StatusError)
	}
	if evt.ConversationID != "chan:work" {
		t.Errorf("conversation_id = %q, want chan:work", evt.ConversationID)
	}
	// Partial success — summary should mention "delivered"
	if evt.Summary != "Schedule daily-check broadcast: 1/2 delivered, 1 failed" {
		t.Errorf("summary = %q", evt.Summary)
	}
}

func TestEmitBroadcastFailure_AllFailed(t *testing.T) {
	em := &broadcastMockEmitter{}
	configmcp.EmitBroadcastFailure(context.Background(), em, true, "daily-check", "@work", "chan:work", 0, 2, "timeout")

	events := em.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Summary != "Schedule daily-check broadcast: 2/2 targets failed" {
		t.Errorf("summary = %q", events[0].Summary)
	}
}

func TestEmitBroadcastFailure_NoBroadcast(t *testing.T) {
	em := &broadcastMockEmitter{}
	configmcp.EmitBroadcastFailure(context.Background(), em, false, "x", "@y", "chan:y", 0, 1, "err")

	if len(em.Events()) != 0 {
		t.Error("expected no events when broadcast=false")
	}
}

func TestEmitBroadcastFailure_NoFailures(t *testing.T) {
	em := &broadcastMockEmitter{}
	configmcp.EmitBroadcastFailure(context.Background(), em, true, "x", "@y", "chan:y", 2, 0, "")

	if len(em.Events()) != 0 {
		t.Error("expected no events when failed=0")
	}
}

func TestEmitBroadcastFailure_NilAuditor(t *testing.T) {
	// Should not panic.
	configmcp.EmitBroadcastFailure(context.Background(), nil, true, "x", "@y", "chan:y", 0, 1, "err")
}
