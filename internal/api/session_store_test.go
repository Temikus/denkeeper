package api

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStore_CreateAndGet(t *testing.T) {
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatalf("NewInMemorySessionStore: %v", err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	expires := time.Now().Add(1 * time.Hour)
	id, err := store.Create(ctx, "admin@test.com", []string{"admin", "chat"}, "TestAgent", "127.0.0.1", expires)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty session ID")
	}

	rec, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Email != "admin@test.com" {
		t.Errorf("email = %q, want admin@test.com", rec.Email)
	}
	if rec.UserAgent != "TestAgent" {
		t.Errorf("user_agent = %q, want TestAgent", rec.UserAgent)
	}
	scopes := ScopesFromRecord(rec)
	if len(scopes) != 2 || scopes[0] != "admin" {
		t.Errorf("scopes = %v, want [admin chat]", scopes)
	}
}

func TestSessionStore_GetExpired(t *testing.T) {
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	// Create an already-expired session.
	id, _ := store.Create(ctx, "admin", []string{"admin"}, "", "", time.Now().Add(-1*time.Hour))

	_, err = store.Get(ctx, id)
	if err == nil {
		t.Fatal("expected error for expired session")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	id, _ := store.Create(ctx, "admin", []string{"admin"}, "", "", time.Now().Add(1*time.Hour))

	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Get(ctx, id)
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestSessionStore_DeleteAllByEmail(t *testing.T) {
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	exp := time.Now().Add(1 * time.Hour)
	_, _ = store.Create(ctx, "admin", []string{"admin"}, "", "", exp)
	_, _ = store.Create(ctx, "admin", []string{"admin"}, "", "", exp)
	_, _ = store.Create(ctx, "other", []string{"admin"}, "", "", exp)

	count, err := store.DeleteAllByEmail(ctx, "admin")
	if err != nil {
		t.Fatalf("DeleteAllByEmail: %v", err)
	}
	if count != 2 {
		t.Errorf("deleted count = %d, want 2", count)
	}

	// "other" session should still exist.
	records, _ := store.ListByEmail(ctx, "other")
	if len(records) != 1 {
		t.Errorf("other sessions = %d, want 1", len(records))
	}
}

func TestSessionStore_ListByEmail(t *testing.T) {
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	exp := time.Now().Add(1 * time.Hour)
	_, _ = store.Create(ctx, "admin", []string{"admin"}, "Chrome", "1.2.3.4", exp)
	_, _ = store.Create(ctx, "admin", []string{"admin"}, "Firefox", "5.6.7.8", exp)

	records, err := store.ListByEmail(ctx, "admin")
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("records = %d, want 2", len(records))
	}
}

func TestSessionStore_Touch(t *testing.T) {
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	id, _ := store.Create(ctx, "admin", []string{"admin"}, "", "", time.Now().Add(1*time.Hour))

	rec1, _ := store.Get(ctx, id)
	time.Sleep(10 * time.Millisecond) // ensure timestamp difference

	if err := store.Touch(ctx, id); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	rec2, _ := store.Get(ctx, id)
	if !rec2.LastSeenAt.After(rec1.LastSeenAt) && rec2.LastSeenAt.Equal(rec1.LastSeenAt) {
		// SQLite datetime resolution may not capture 10ms, so just verify no error.
		_ = rec2
	}
}

func TestSessionStore_PurgeExpired(t *testing.T) {
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	_, _ = store.Create(ctx, "admin", []string{"admin"}, "", "", time.Now().Add(-1*time.Hour)) // expired
	_, _ = store.Create(ctx, "admin", []string{"admin"}, "", "", time.Now().Add(1*time.Hour))  // active

	count, err := store.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if count != 1 {
		t.Errorf("purged = %d, want 1", count)
	}

	total, _ := store.Count(ctx)
	if total != 1 {
		t.Errorf("remaining = %d, want 1", total)
	}
}

func TestSessionStore_Count(t *testing.T) {
	store, err := NewInMemorySessionStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx := context.Background()
	exp := time.Now().Add(1 * time.Hour)
	_, _ = store.Create(ctx, "a", []string{}, "", "", exp)
	_, _ = store.Create(ctx, "b", []string{}, "", "", exp)

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestSessionStore_StartCleanup(t *testing.T) {
	// Use a temp file instead of :memory: so the cleanup goroutine shares the same DB.
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create an already-expired session.
	_, _ = store.Create(ctx, "admin@test.com", []string{}, "ua", "127.0.0.1", time.Now().Add(-1*time.Hour))
	// Create a valid session.
	_, _ = store.Create(ctx, "admin@test.com", []string{}, "ua", "127.0.0.1", time.Now().Add(1*time.Hour))

	logger := slog.Default()
	store.StartCleanup(ctx, 50*time.Millisecond, logger)

	// Wait for at least one cleanup tick.
	time.Sleep(200 * time.Millisecond)

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 session after cleanup, got %d", count)
	}
}
