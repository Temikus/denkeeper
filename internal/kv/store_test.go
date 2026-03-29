package kv

import (
	"context"
	"testing"
	"time"
)

func mustStore(t *testing.T, opts ...Option) *SQLiteStore {
	t.Helper()
	s, err := NewInMemoryStore(opts...)
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestGet_NotFound(t *testing.T) {
	s := mustStore(t)
	_, ok, err := s.Get(context.Background(), "agent1", "missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing key")
	}
}

func TestSetAndGet(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "agent1", "greeting", "hello", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, ok, err := s.Get(ctx, "agent1", "greeting")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if val != "hello" {
		t.Errorf("Get = %q, want %q", val, "hello")
	}
}

func TestSet_Upsert(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "agent1", "k", "v1", 0); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set(ctx, "agent1", "k", "v2", 0); err != nil {
		t.Fatalf("Set v2: %v", err)
	}

	val, _, err := s.Get(ctx, "agent1", "k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "v2" {
		t.Errorf("Get = %q, want %q", val, "v2")
	}
}

func TestGet_AgentIsolation(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "agent1", "secret", "a1-data", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	_, ok, err := s.Get(ctx, "agent2", "secret")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("agent2 should not see agent1's key")
	}
}

func TestGet_ExpiredKey(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	// Set with a very short TTL — SQLite datetime resolution is seconds,
	// so we need to manipulate the DB directly.
	if err := s.Set(ctx, "agent1", "temp", "val", time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Force expiry by updating expires_at to the past.
	_, err := s.db.Exec(`UPDATE kv SET expires_at = datetime('now', '-1 second') WHERE key = 'temp'`)
	if err != nil {
		t.Fatalf("manual expire: %v", err)
	}

	_, ok, err := s.Get(ctx, "agent1", "temp")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected expired key to be invisible")
	}
}

func TestDelete(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "agent1", "k", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete(ctx, "agent1", "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok, _ := s.Get(ctx, "agent1", "k")
	if ok {
		t.Error("key should be deleted")
	}
}

func TestDelete_Nonexistent(t *testing.T) {
	s := mustStore(t)
	if err := s.Delete(context.Background(), "agent1", "nope"); err != nil {
		t.Errorf("Delete nonexistent key: %v", err)
	}
}

func TestList_Empty(t *testing.T) {
	s := mustStore(t)
	entries, err := s.List(context.Background(), "agent1", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty list, got %d entries", len(entries))
	}
}

func TestList_WithPrefix(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "agent1", "lock:a", "1", 0)
	_ = s.Set(ctx, "agent1", "lock:b", "2", 0)
	_ = s.Set(ctx, "agent1", "cache:x", "3", 0)

	entries, err := s.List(ctx, "agent1", "lock:")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Key != "lock:a" || entries[1].Key != "lock:b" {
		t.Errorf("unexpected keys: %v, %v", entries[0].Key, entries[1].Key)
	}
}

func TestList_ExcludesExpired(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "agent1", "live", "yes", 0)
	_ = s.Set(ctx, "agent1", "dead", "no", time.Hour)

	_, _ = s.db.Exec(`UPDATE kv SET expires_at = datetime('now', '-1 second') WHERE key = 'dead'`)

	entries, err := s.List(ctx, "agent1", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 live entry, got %d", len(entries))
	}
	if entries[0].Key != "live" {
		t.Errorf("expected key 'live', got %q", entries[0].Key)
	}
}

func TestSetNX_AcquiresNewKey(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	ok, err := s.SetNX(ctx, "agent1", "lock:x", "owner1", 5*time.Minute)
	if err != nil {
		t.Fatalf("SetNX: %v", err)
	}
	if !ok {
		t.Error("expected SetNX to succeed on new key")
	}

	val, found, _ := s.Get(ctx, "agent1", "lock:x")
	if !found || val != "owner1" {
		t.Errorf("Get after SetNX: found=%v, val=%q", found, val)
	}
}

func TestSetNX_RejectsExistingKey(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	_, _ = s.SetNX(ctx, "agent1", "lock:x", "owner1", 0)

	ok, err := s.SetNX(ctx, "agent1", "lock:x", "owner2", 0)
	if err != nil {
		t.Fatalf("SetNX: %v", err)
	}
	if ok {
		t.Error("expected SetNX to fail on existing key")
	}

	val, _, _ := s.Get(ctx, "agent1", "lock:x")
	if val != "owner1" {
		t.Errorf("value should be original owner, got %q", val)
	}
}

func TestSetNX_AllowsExpiredKey(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	_, _ = s.SetNX(ctx, "agent1", "lock:x", "old", time.Hour)
	_, _ = s.db.Exec(`UPDATE kv SET expires_at = datetime('now', '-1 second') WHERE key = 'lock:x'`)

	ok, err := s.SetNX(ctx, "agent1", "lock:x", "new", time.Hour)
	if err != nil {
		t.Fatalf("SetNX after expiry: %v", err)
	}
	if !ok {
		t.Error("expected SetNX to succeed after key expired")
	}

	val, found, _ := s.Get(ctx, "agent1", "lock:x")
	if !found || val != "new" {
		t.Errorf("Get after SetNX on expired: found=%v, val=%q", found, val)
	}
}

func TestCleanup_RemovesExpired(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "agent1", "keep", "yes", 0)
	_ = s.Set(ctx, "agent1", "remove", "no", time.Hour)
	_, _ = s.db.Exec(`UPDATE kv SET expires_at = datetime('now', '-1 second') WHERE key = 'remove'`)

	if err := s.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Expired key should be physically deleted.
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM kv WHERE agent_name = 'agent1'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row after cleanup, got %d", count)
	}
}

func TestSet_ValueSizeLimit(t *testing.T) {
	s := mustStore(t, WithMaxValueBytes(10))
	err := s.Set(context.Background(), "agent1", "big", "12345678901", 0)
	if err == nil {
		t.Fatal("expected error for oversized value")
	}
}

func TestSet_KeyCountLimit(t *testing.T) {
	s := mustStore(t, WithMaxKeysPerAgent(2))
	ctx := context.Background()

	_ = s.Set(ctx, "agent1", "k1", "v", 0)
	_ = s.Set(ctx, "agent1", "k2", "v", 0)

	err := s.Set(ctx, "agent1", "k3", "v", 0)
	if err == nil {
		t.Fatal("expected error for exceeding key limit")
	}

	// Updating existing key should still work.
	if err := s.Set(ctx, "agent1", "k1", "updated", 0); err != nil {
		t.Errorf("update existing key should succeed: %v", err)
	}
}

func TestSetNX_ValueSizeLimit(t *testing.T) {
	s := mustStore(t, WithMaxValueBytes(5))
	_, err := s.SetNX(context.Background(), "agent1", "k", "123456", 0)
	if err == nil {
		t.Fatal("expected error for oversized value")
	}
}

func TestSetNX_KeyCountLimit(t *testing.T) {
	s := mustStore(t, WithMaxKeysPerAgent(1))
	ctx := context.Background()

	_, _ = s.SetNX(ctx, "agent1", "k1", "v", 0)

	ok, err := s.SetNX(ctx, "agent1", "k2", "v", 0)
	if err == nil && ok {
		t.Fatal("expected error or rejection for exceeding key limit")
	}
}
