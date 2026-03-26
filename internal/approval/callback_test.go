package approval

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func newTestHandler(t *testing.T) (*Handler, *Manager, *SQLiteStore) {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mgr := NewManager(store, logger)
	h := NewCallbackHandler(mgr, logger)
	return h, mgr, store
}

func submitTestApproval(t *testing.T, mgr *Manager, kind ActionKind) *Request {
	t.Helper()
	ctx := context.Background()
	req, err := mgr.Submit(
		ctx,
		"default",
		kind,
		"Test approval",
		"payload",
		"12345",
		"telegram",
		"conv-1",
		func(_ context.Context, _ string) error { return nil },
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return req
}

func TestHandler_Resolve_NonApprovalCallback(t *testing.T) {
	h, _, _ := newTestHandler(t)
	text, err := h.Resolve(context.Background(), "some:other:data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "" {
		t.Errorf("expected empty response for non-approval callback, got %q", text)
	}
}

func TestHandler_Resolve_Approve(t *testing.T) {
	h, mgr, _ := newTestHandler(t)
	req := submitTestApproval(t, mgr, ActionKindUserUpdate)

	text, err := h.Resolve(context.Background(), req.CallbackData+":approve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "✅") {
		t.Errorf("expected approval confirmation, got %q", text)
	}
	if !strings.Contains(text, "Test approval") {
		t.Errorf("expected summary in response, got %q", text)
	}
}

func TestHandler_Resolve_Deny(t *testing.T) {
	h, mgr, _ := newTestHandler(t)
	req := submitTestApproval(t, mgr, ActionKindUserUpdate)

	text, err := h.Resolve(context.Background(), req.CallbackData+":deny")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "❌") {
		t.Errorf("expected denial confirmation, got %q", text)
	}
	if !strings.Contains(text, "Test approval") {
		t.Errorf("expected summary in response, got %q", text)
	}
}

func TestHandler_Resolve_UnknownCallback(t *testing.T) {
	h, _, _ := newTestHandler(t)
	text, err := h.Resolve(context.Background(), "appr:deadbeef:approve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unknown approval IDs are silently ignored (logged as warn).
	if text != "" {
		t.Errorf("expected empty response for unknown approval, got %q", text)
	}
}

func TestHandler_Resolve_AlreadyApproved(t *testing.T) {
	h, mgr, _ := newTestHandler(t)
	req := submitTestApproval(t, mgr, ActionKindCreateSkill)

	// Resolve once.
	if _, err := h.Resolve(context.Background(), req.CallbackData+":approve"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Resolve again — should return stale callback message.
	text, err := h.Resolve(context.Background(), req.CallbackData+":approve")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if !strings.Contains(text, "Already approved") {
		t.Errorf("expected stale-approved message, got %q", text)
	}
}

func TestHandler_Resolve_AlreadyDenied(t *testing.T) {
	h, mgr, _ := newTestHandler(t)
	req := submitTestApproval(t, mgr, ActionKindModifySchedule)

	if _, err := h.Resolve(context.Background(), req.CallbackData+":deny"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	text, err := h.Resolve(context.Background(), req.CallbackData+":deny")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if !strings.Contains(text, "Already denied") {
		t.Errorf("expected stale-denied message, got %q", text)
	}
}

func TestHandler_Resolve_Expired(t *testing.T) {
	h, mgr, store := newTestHandler(t)
	req := submitTestApproval(t, mgr, ActionKindUserUpdate)

	// Force-expire the approval via the store directly.
	if err := store.Resolve(context.Background(), req.ID, StatusExpired, "expired"); err != nil {
		t.Fatalf("force expire: %v", err)
	}

	text, err := h.Resolve(context.Background(), req.CallbackData+":approve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "expired") {
		t.Errorf("expected expired message, got %q", text)
	}
}

func TestHandler_Resolve_ActionExecuted(t *testing.T) {
	h, mgr, _ := newTestHandler(t)

	actionCalled := false
	ctx := context.Background()
	req, err := mgr.Submit(
		ctx,
		"default",
		ActionKindUserUpdate,
		"Profile update",
		"new content",
		"12345",
		"telegram",
		"conv-1",
		func(_ context.Context, payload string) error {
			if payload == "new content" {
				actionCalled = true
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if _, err := h.Resolve(ctx, req.CallbackData+":approve"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !actionCalled {
		t.Error("action closure was not called on approval")
	}
}

func TestHandler_Resolve_ExpiredByTTL(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mgr := NewManager(store, logger)
	h := NewCallbackHandler(mgr, logger)

	ctx := context.Background()

	// Submit with an expiry in the past.
	past := time.Now().UTC().Add(-time.Second)
	req := Request{
		ID:             "expiredid",
		AgentName:      "default",
		Kind:           ActionKindUserUpdate,
		Status:         StatusPending,
		Summary:        "Stale request",
		Payload:        "data",
		CallbackData:   "appr:expiredid",
		ExternalID:     "12345",
		AdapterName:    "telegram",
		ConversationID: "conv-1",
		ExpiresAt:      &past,
	}
	if _, err := store.Create(ctx, req); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Run expiry worker manually.
	if _, err := store.ExpireBefore(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("ExpireBefore: %v", err)
	}

	text, err := h.Resolve(ctx, "appr:expiredid:approve")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(text, "expired") {
		t.Errorf("expected expired message, got %q", text)
	}
}
