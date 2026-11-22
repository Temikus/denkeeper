package kv

import (
	"context"
	"reflect"
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

func TestGetEntry_ReturnsMetadata(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()

	if _, ok, err := s.GetEntry(ctx, "agent1", "missing"); err != nil || ok {
		t.Fatalf("GetEntry missing: ok=%v err=%v, want ok=false", ok, err)
	}
	if err := s.Set(ctx, "agent1", "greeting", "hello", time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}
	e, ok, err := s.GetEntry(ctx, "agent1", "greeting")
	if err != nil || !ok {
		t.Fatalf("GetEntry: ok=%v err=%v", ok, err)
	}
	if e.Key != "greeting" || e.Value != "hello" {
		t.Errorf("entry = %+v, want key/value greeting/hello", e)
	}
	if e.UpdatedAt.IsZero() || time.Since(e.UpdatedAt) > time.Minute {
		t.Errorf("UpdatedAt = %v, want a recent timestamp", e.UpdatedAt)
	}
	if e.ExpiresAt == nil {
		t.Error("ExpiresAt = nil, want the TTL expiry")
	}
}

var backfillDefaults = map[string]time.Duration{
	"log:":          720 * time.Hour,
	"log:research:": 168 * time.Hour,
	"cache:":        time.Hour,
}

func mustSet(t *testing.T, s *SQLiteStore, agent, key string, ttl time.Duration) {
	t.Helper()
	if err := s.Set(context.Background(), agent, key, "v-"+key, ttl); err != nil {
		t.Fatalf("Set %s: %v", key, err)
	}
}

// backdate stamps a key's timestamps directly, since Set always writes now.
func backdate(t *testing.T, s *SQLiteStore, agent, key string, at time.Time) {
	t.Helper()
	stamp := at.UTC().Format(timeLayout)
	if _, err := s.db.Exec(`UPDATE kv SET created_at = ?, updated_at = ? WHERE agent_name = ? AND key = ?`,
		stamp, stamp, agent, key); err != nil {
		t.Fatalf("backdate %s: %v", key, err)
	}
}

// rawExpiry reads expires_at as stored text, bypassing the expiry filter, so
// the layout the text comparison against datetime('now') relies on is checked.
func rawExpiry(t *testing.T, s *SQLiteStore, agent, key string) (string, bool) {
	t.Helper()
	var exp *string
	if err := s.db.QueryRow(`SELECT CAST(expires_at AS TEXT) FROM kv WHERE agent_name = ? AND key = ?`,
		agent, key).Scan(&exp); err != nil {
		t.Fatalf("read expires_at %s: %v", key, err)
	}
	if exp == nil {
		return "", false
	}
	return *exp, true
}

func mustBackfill(t *testing.T, s *SQLiteStore, dryRun bool) []BackfillCount {
	t.Helper()
	counts, err := s.BackfillTTL(context.Background(), backfillDefaults, dryRun)
	if err != nil {
		t.Fatalf("BackfillTTL(dryRun=%v): %v", dryRun, err)
	}
	return counts
}

func TestBackfillTTL_SetsUpdatedAtPlusTTL(t *testing.T) {
	s := mustStore(t)
	mustSet(t, s, "agent1", "log:heartbeat:a", 0)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	backdate(t, s, "agent1", "log:heartbeat:a", at)

	counts := mustBackfill(t, s, false)

	want := []BackfillCount{{Agent: "agent1", Namespace: "log:", TTL: 720 * time.Hour, Keys: 1}}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("counts = %+v, want %+v", counts, want)
	}
	got, ok := rawExpiry(t, s, "agent1", "log:heartbeat:a")
	if wantExp := at.Add(720 * time.Hour).Format(timeLayout); !ok || got != wantExp {
		t.Errorf("expires_at = %q (set=%v), want %q", got, ok, wantExp)
	}
}

func TestBackfillTTL_LongestPrefixWins(t *testing.T) {
	s := mustStore(t)
	mustSet(t, s, "agent1", "log:research:x", 0)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	backdate(t, s, "agent1", "log:research:x", at)

	counts := mustBackfill(t, s, false)

	if len(counts) != 1 || counts[0].Namespace != "log:research:" || counts[0].TTL != 168*time.Hour {
		t.Errorf("counts = %+v, want one log:research: row at 168h", counts)
	}
	if got, _ := rawExpiry(t, s, "agent1", "log:research:x"); got != at.Add(168*time.Hour).Format(timeLayout) {
		t.Errorf("expires_at = %q, want the log:research: TTL", got)
	}
}

func TestBackfillTTL_UnconfiguredNamespacesUntouched(t *testing.T) {
	s := mustStore(t)
	mustSet(t, s, "agent1", "pref:theme", 0)
	mustSet(t, s, "agent1", "state:cursor", 0)

	if counts := mustBackfill(t, s, false); len(counts) != 0 {
		t.Errorf("counts = %+v, want none", counts)
	}
	for _, key := range []string{"pref:theme", "state:cursor"} {
		if got, ok := rawExpiry(t, s, "agent1", key); ok {
			t.Errorf("%s expires_at = %q, want NULL", key, got)
		}
	}
}

func TestBackfillTTL_ExistingExpiryKept(t *testing.T) {
	s := mustStore(t)
	mustSet(t, s, "agent1", "log:x", 5*time.Hour)
	before, _ := rawExpiry(t, s, "agent1", "log:x")

	if counts := mustBackfill(t, s, false); len(counts) != 0 {
		t.Errorf("counts = %+v, want none", counts)
	}
	if after, _ := rawExpiry(t, s, "agent1", "log:x"); after != before {
		t.Errorf("expires_at changed from %q to %q", before, after)
	}
}

func TestBackfillTTL_ValueAndTimestampsUnchanged(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	mustSet(t, s, "agent1", "cache:page", 0)
	backdate(t, s, "agent1", "cache:page", time.Now().UTC().Add(-10*time.Minute).Truncate(time.Second))
	before, _, err := s.GetEntry(ctx, "agent1", "cache:page")
	if err != nil {
		t.Fatal(err)
	}

	mustBackfill(t, s, false)

	after, ok, err := s.GetEntry(ctx, "agent1", "cache:page")
	if err != nil || !ok {
		t.Fatalf("GetEntry after: ok=%v err=%v", ok, err)
	}
	if after.Value != before.Value || !after.CreatedAt.Equal(before.CreatedAt) || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("entry changed: before %+v, after %+v", before, after)
	}
	if after.ExpiresAt == nil {
		t.Error("ExpiresAt = nil, want the backfilled expiry")
	}
}

func TestBackfillTTL_PastExpiryHiddenThenCleaned(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	mustSet(t, s, "agent1", "log:old", 0)
	backdate(t, s, "agent1", "log:old", time.Now().UTC().Add(-800*time.Hour))

	counts := mustBackfill(t, s, false)

	if len(counts) != 1 || counts[0].Keys != 1 || counts[0].AlreadyExpired != 1 {
		t.Errorf("counts = %+v, want 1 key, 1 already expired", counts)
	}
	if _, ok, err := s.Get(ctx, "agent1", "log:old"); err != nil || ok {
		t.Errorf("Get after backfill: ok=%v err=%v, want hidden", ok, err)
	}
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM kv WHERE key = 'log:old'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows after Cleanup = %d (err %v), want 0", n, err)
	}
}

// SQL LIKE would match both: it ignores ASCII case and treats _ as a wildcard.
func TestBackfillTTL_NoCaseOrWildcardMatch(t *testing.T) {
	s := mustStore(t)
	defaults := map[string]time.Duration{"log:": time.Hour, "a_b:": time.Hour}
	mustSet(t, s, "agent1", "LOG:x", 0)
	mustSet(t, s, "agent1", "axb:1", 0)

	counts, err := s.BackfillTTL(context.Background(), defaults, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Errorf("counts = %+v, want none", counts)
	}
	for _, key := range []string{"LOG:x", "axb:1"} {
		if got, ok := rawExpiry(t, s, "agent1", key); ok {
			t.Errorf("%s expires_at = %q, want NULL", key, got)
		}
	}
}

func TestBackfillTTL_DryRunCountsWithoutWriting(t *testing.T) {
	s := mustStore(t)
	mustSet(t, s, "agent1", "log:a", 0)
	mustSet(t, s, "agent1", "cache:b", 0)

	dry := mustBackfill(t, s, true)
	for _, key := range []string{"log:a", "cache:b"} {
		if got, ok := rawExpiry(t, s, "agent1", key); ok {
			t.Errorf("dry run wrote %s expires_at = %q", key, got)
		}
	}
	if real := mustBackfill(t, s, false); !reflect.DeepEqual(dry, real) {
		t.Errorf("dry run counts %+v differ from the real run's %+v", dry, real)
	}
}

func TestBackfillTTL_SecondRunChangesNothing(t *testing.T) {
	s := mustStore(t)
	mustSet(t, s, "agent1", "log:a", 0)

	if first := mustBackfill(t, s, false); len(first) != 1 {
		t.Fatalf("first run counts = %+v, want one row", first)
	}
	if second := mustBackfill(t, s, false); len(second) != 0 {
		t.Errorf("second run counts = %+v, want none", second)
	}
}

func TestBackfillTTL_AgentsCountedSeparately(t *testing.T) {
	s := mustStore(t)
	mustSet(t, s, "zeta", "log:a", 0)
	mustSet(t, s, "alpha", "log:a", 0)
	mustSet(t, s, "alpha", "log:b", 0)

	counts := mustBackfill(t, s, false)

	want := []BackfillCount{
		{Agent: "alpha", Namespace: "log:", TTL: 720 * time.Hour, Keys: 2},
		{Agent: "zeta", Namespace: "log:", TTL: 720 * time.Hour, Keys: 1},
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("counts = %+v, want %+v", counts, want)
	}
}
