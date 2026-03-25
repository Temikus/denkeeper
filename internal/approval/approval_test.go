package approval

import (
	"context"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testRequest(id string) Request {
	return Request{
		ID:             id,
		AgentName:      "default",
		Kind:           ActionKindUserUpdate,
		Summary:        "Update user profile",
		Payload:        "# User\n\nTest user.",
		CallbackData:   "appr:" + id,
		ExternalID:     "12345",
		AdapterName:    "telegram",
		ConversationID: "default:telegram:12345",
	}
}

func TestSQLiteStore_Create_AssignsID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	req := testRequest("abc123")
	id, err := s.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "abc123" {
		t.Errorf("expected id %q, got %q", "abc123", id)
	}
}

func TestSQLiteStore_Get_Found(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	req := testRequest("get1")
	if _, err := s.Create(ctx, req); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "get1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "get1" {
		t.Errorf("expected id %q, got %q", "get1", got.ID)
	}
	if got.Status != StatusPending {
		t.Errorf("expected status pending, got %q", got.Status)
	}
	if got.Summary != req.Summary {
		t.Errorf("summary mismatch: want %q, got %q", req.Summary, got.Summary)
	}
}

func TestSQLiteStore_Get_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_List_FilterByStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1 := testRequest("list1")
	r2 := testRequest("list2")
	if _, err := s.Create(ctx, r1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, r2); err != nil {
		t.Fatal(err)
	}
	if err := s.Resolve(ctx, "list1", StatusApproved, "test"); err != nil {
		t.Fatal(err)
	}

	pending, err := s.List(ctx, StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "list2" {
		t.Errorf("expected 1 pending (list2), got %v", pending)
	}

	approved, err := s.List(ctx, StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if len(approved) != 1 || approved[0].ID != "list1" {
		t.Errorf("expected 1 approved (list1), got %v", approved)
	}

	all, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 total, got %d", len(all))
	}
}

func TestSQLiteStore_Resolve_Success(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	req := testRequest("res1")
	if _, err := s.Create(ctx, req); err != nil {
		t.Fatal(err)
	}

	if err := s.Resolve(ctx, "res1", StatusApproved, "api"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, err := s.Get(ctx, "res1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusApproved {
		t.Errorf("expected approved, got %q", got.Status)
	}
	if got.ResolvedBy != "api" {
		t.Errorf("expected resolvedBy %q, got %q", "api", got.ResolvedBy)
	}
	if got.ResolvedAt == nil {
		t.Error("expected ResolvedAt to be set")
	}
}

func TestSQLiteStore_Resolve_AlreadyResolved(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	req := testRequest("res2")
	if _, err := s.Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := s.Resolve(ctx, "res2", StatusApproved, "api"); err != nil {
		t.Fatal(err)
	}

	err := s.Resolve(ctx, "res2", StatusDenied, "api")
	if err != ErrAlreadyResolved {
		t.Errorf("expected ErrAlreadyResolved, got %v", err)
	}
}

func TestSQLiteStore_Resolve_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.Resolve(ctx, "missing", StatusApproved, "api")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_ResolveByCallbackPrefix_Approve(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	req := testRequest("cb1")
	if _, err := s.Create(ctx, req); err != nil {
		t.Fatal(err)
	}

	got, err := s.ResolveByCallbackPrefix(ctx, "appr:cb1", StatusApproved, "telegram")
	if err != nil {
		t.Fatalf("ResolveByCallbackPrefix: %v", err)
	}
	if got.Status != StatusApproved {
		t.Errorf("expected approved, got %q", got.Status)
	}
}

func TestSQLiteStore_ResolveByCallbackPrefix_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.ResolveByCallbackPrefix(ctx, "appr:nope", StatusApproved, "telegram")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_ExpirePending(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"exp1", "exp2", "exp3"} {
		if _, err := s.Create(ctx, testRequest(id)); err != nil {
			t.Fatal(err)
		}
	}
	// Resolve one so it shouldn't be expired.
	if err := s.Resolve(ctx, "exp1", StatusApproved, "api"); err != nil {
		t.Fatal(err)
	}

	n, err := s.ExpirePending(ctx)
	if err != nil {
		t.Fatalf("ExpirePending: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 expired, got %d", n)
	}

	pending, err := s.List(ctx, StatusPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after expire, got %d", len(pending))
	}

	expired, err := s.List(ctx, StatusExpired)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 2 {
		t.Errorf("expected 2 expired rows, got %d", len(expired))
	}
}

func TestSQLiteStore_CreatedAt_Set(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	before := time.Now().Add(-time.Second)
	if _, err := s.Create(ctx, testRequest("ts1")); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "ts1")
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt.Before(before) {
		t.Errorf("CreatedAt %v is before test start %v", got.CreatedAt, before)
	}
}
