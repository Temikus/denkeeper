package approval

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewManager(store, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func TestManager_Submit_AssignsID(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	req, err := m.Submit(ctx, "default", ActionKindUserUpdate,
		"Test summary", "payload", "123", "telegram", "conv1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if req.ID == "" {
		t.Error("expected non-empty ID")
	}
	if req.CallbackData != "appr:"+req.ID {
		t.Errorf("unexpected CallbackData %q", req.CallbackData)
	}
	if req.Status != StatusPending {
		t.Errorf("expected pending, got %q", req.Status)
	}
}

func TestManager_Submit_RegistersAction(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	req, err := m.Submit(ctx, "default", ActionKindUserUpdate,
		"summary", "payload", "123", "telegram", "conv1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}

	// Action should be in registry.
	fn, ok := m.registry.Pop(req.ID)
	if !ok || fn == nil {
		t.Error("action not registered")
	}
}

func TestManager_Resolve_Approved_CallsAction(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	var receivedPayload string
	req, err := m.Submit(ctx, "default", ActionKindUserUpdate,
		"summary", "the-payload", "123", "telegram", "conv1",
		func(_ context.Context, p string) error {
			receivedPayload = p
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := m.Resolve(ctx, req.ID, true, "test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != StatusApproved {
		t.Errorf("expected approved, got %q", resolved.Status)
	}
	if receivedPayload != "the-payload" {
		t.Errorf("action received wrong payload: %q", receivedPayload)
	}
}

func TestManager_Resolve_Denied_SkipsAction(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	called := false
	req, err := m.Submit(ctx, "default", ActionKindUserUpdate,
		"summary", "payload", "123", "telegram", "conv1",
		func(_ context.Context, _ string) error {
			called = true
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := m.Resolve(ctx, req.ID, false, "test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != StatusDenied {
		t.Errorf("expected denied, got %q", resolved.Status)
	}
	if called {
		t.Error("action should not be called on denial")
	}
}

func TestManager_Resolve_NotFound(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	_, err := m.Resolve(ctx, "missing", true, "test")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestManager_Resolve_AlreadyResolved(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	req, _ := m.Submit(ctx, "default", ActionKindUserUpdate,
		"s", "p", "123", "telegram", "c",
		func(_ context.Context, _ string) error { return nil },
	)
	if _, err := m.Resolve(ctx, req.ID, true, "test"); err != nil {
		t.Fatal(err)
	}

	_, err := m.Resolve(ctx, req.ID, false, "test")
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Errorf("expected ErrAlreadyResolved, got %v", err)
	}
}

func TestManager_Resolve_ActionPoppedOnce(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	callCount := 0
	req, _ := m.Submit(ctx, "default", ActionKindUserUpdate,
		"s", "p", "123", "telegram", "c",
		func(_ context.Context, _ string) error {
			callCount++
			return nil
		},
	)

	if _, err := m.Resolve(ctx, req.ID, true, "test"); err != nil {
		t.Fatal(err)
	}

	// Re-registering to attempt double-invoke.
	m.registry.Register(req.ID, func(_ context.Context, _ string) error {
		callCount++
		return nil
	})
	// But resolve should return ErrAlreadyResolved, not invoke again.
	_, err := m.Resolve(ctx, req.ID, true, "test")
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Errorf("expected ErrAlreadyResolved, got %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected action called once, got %d", callCount)
	}
}

func TestManager_ResolveByCallback_Approve(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	var executed string
	req, _ := m.Submit(ctx, "default", ActionKindUserUpdate,
		"s", "my-payload", "123", "telegram", "c",
		func(_ context.Context, p string) error {
			executed = p
			return nil
		},
	)

	resolved, err := m.ResolveByCallback(ctx, req.CallbackData+":approve", "telegram")
	if err != nil {
		t.Fatalf("ResolveByCallback: %v", err)
	}
	if resolved.Status != StatusApproved {
		t.Errorf("expected approved, got %q", resolved.Status)
	}
	if executed != "my-payload" {
		t.Errorf("expected payload %q, got %q", "my-payload", executed)
	}
}

func TestManager_ResolveByCallback_Deny(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	called := false
	req, _ := m.Submit(ctx, "default", ActionKindUserUpdate,
		"s", "p", "123", "telegram", "c",
		func(_ context.Context, _ string) error {
			called = true
			return nil
		},
	)

	resolved, err := m.ResolveByCallback(ctx, req.CallbackData+":deny", "telegram")
	if err != nil {
		t.Fatalf("ResolveByCallback: %v", err)
	}
	if resolved.Status != StatusDenied {
		t.Errorf("expected denied, got %q", resolved.Status)
	}
	if called {
		t.Error("action should not be called on denial")
	}
}

func TestManager_ResolveByCallback_Unknown(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	_, err := m.ResolveByCallback(ctx, "unknown:data", "telegram")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown callback, got %v", err)
	}
}

func TestManager_List(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	for range 3 {
		if _, err := m.Submit(ctx, "default", ActionKindUserUpdate,
			"s", "p", "123", "telegram", "c",
			func(_ context.Context, _ string) error { return nil },
		); err != nil {
			t.Fatal(err)
		}
	}

	all, err := m.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	pending, err := m.List(ctx, StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Errorf("expected 3 pending, got %d", len(pending))
	}
}

func TestManager_WaitForResolution_Approved(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	req, err := m.Submit(ctx, "default", ActionKindToolCall,
		"Execute tool", "args", "ext-1", "telegram", "conv-1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Approve in a goroutine.
	go func() {
		_, _ = m.Resolve(ctx, req.ID, true, "operator")
	}()

	status := m.WaitForResolution(ctx, req.ID)
	if status != StatusApproved {
		t.Errorf("WaitForResolution = %q, want %q", status, StatusApproved)
	}
}

func TestManager_WaitForResolution_Denied(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	req, err := m.Submit(ctx, "default", ActionKindToolCall,
		"Execute tool", "args", "ext-1", "telegram", "conv-1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	go func() {
		_, _ = m.Resolve(ctx, req.ID, false, "operator")
	}()

	status := m.WaitForResolution(ctx, req.ID)
	if status != StatusDenied {
		t.Errorf("WaitForResolution = %q, want %q", status, StatusDenied)
	}
}

func TestManager_WaitForResolution_ContextCancelled(t *testing.T) {
	m := newTestManager(t)

	req, err := m.Submit(context.Background(), "default", ActionKindToolCall,
		"Execute tool", "args", "ext-1", "telegram", "conv-1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	status := m.WaitForResolution(ctx, req.ID)
	if status != StatusExpired {
		t.Errorf("WaitForResolution with cancelled ctx = %q, want %q", status, StatusExpired)
	}
}

func TestManager_WaitForResolution_ViaCallback(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	req, err := m.Submit(ctx, "default", ActionKindToolCall,
		"Execute tool", "args", "ext-1", "telegram", "conv-1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Resolve via callback (simulates Telegram button press).
	go func() {
		_, _ = m.ResolveByCallback(ctx, req.CallbackData+":approve", "user-123")
	}()

	status := m.WaitForResolution(ctx, req.ID)
	if status != StatusApproved {
		t.Errorf("WaitForResolution via callback = %q, want %q", status, StatusApproved)
	}
}

func TestManager_SubmitAndWait_Approved(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	var actionPayload string

	// Resolve the first pending request after a short delay.
	// SubmitAndWait blocks on select{} so the main goroutine holds no
	// SQLite locks when this fires — no contention.
	resolveAfter(t, m, true, 50*time.Millisecond)

	status, req, err := m.SubmitAndWait(ctx, "default", ActionKindToolCall,
		"Run tool X", "tool-payload", "ext-1", "telegram", "conv-1",
		func(_ context.Context, p string) error {
			actionPayload = p
			return nil
		},
	)
	if err != nil {
		t.Fatalf("SubmitAndWait: %v", err)
	}
	if status != StatusApproved {
		t.Errorf("status = %q, want %q", status, StatusApproved)
	}
	if req == nil || req.ID == "" {
		t.Error("expected non-nil request with ID")
	}
	if actionPayload != "tool-payload" {
		t.Errorf("action payload = %q, want %q", actionPayload, "tool-payload")
	}
}

func TestManager_SubmitAndWait_Denied(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	actionCalled := false
	resolveAfter(t, m, false, 50*time.Millisecond)

	status, _, err := m.SubmitAndWait(ctx, "default", ActionKindToolCall,
		"Run tool X", "payload", "ext-1", "telegram", "conv-1",
		func(_ context.Context, _ string) error {
			actionCalled = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("SubmitAndWait: %v", err)
	}
	if status != StatusDenied {
		t.Errorf("status = %q, want %q", status, StatusDenied)
	}
	if actionCalled {
		t.Error("action should not be called on denial")
	}
}

func TestManager_SubmitAndWait_ContextCancelled(t *testing.T) {
	m := newTestManager(t)

	// Use a generous timeout so submit always succeeds even on slow CI.
	// Never resolve the request — the context expiration is what we're testing.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	status, _, err := m.SubmitAndWait(ctx, "default", ActionKindToolCall,
		"Run tool X", "payload", "ext-1", "telegram", "conv-1", nil,
	)
	if status != StatusExpired {
		t.Errorf("status = %q, want %q", status, StatusExpired)
	}
	if err == nil {
		t.Error("expected context error")
	}
}

func TestManager_SubmitAndWait_NilAction(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()

	resolveAfter(t, m, true, 50*time.Millisecond)

	// Should not panic with nil action.
	status, _, err := m.SubmitAndWait(ctx, "default", ActionKindToolCall,
		"Run tool X", "payload", "ext-1", "telegram", "conv-1", nil,
	)
	if err != nil {
		t.Fatalf("SubmitAndWait: %v", err)
	}
	if status != StatusApproved {
		t.Errorf("status = %q, want %q", status, StatusApproved)
	}
}

// resolveAfter polls for the first pending request and resolves it.
// It retries every 10ms for up to 5s after the initial delay.
func resolveAfter(t *testing.T, m *Manager, approve bool, delay time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		time.Sleep(delay)
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			reqs, err := m.List(ctx, StatusPending)
			if err != nil {
				if ctx.Err() != nil {
					return // test ended, DB closed — not an error
				}
				t.Errorf("resolveAfter List: %v", err)
				return
			}
			if len(reqs) > 0 {
				if _, err := m.Resolve(ctx, reqs[0].ID, approve, "operator"); err != nil {
					if ctx.Err() != nil {
						return
					}
					t.Errorf("resolveAfter Resolve: %v", err)
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-deadline:
				t.Errorf("resolveAfter: no pending request within timeout")
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
}
