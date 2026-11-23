package kv

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func setKeys(t *testing.T, s *SQLiteStore, agent string, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if err := s.Set(context.Background(), agent, k, "v", 0); err != nil {
			t.Fatalf("Set %s: %v", k, err)
		}
	}
}

func TestNamespaceStats_GroupsByPrefix(t *testing.T) {
	s := mustStore(t)
	setKeys(t, s, "a1", "log:a", "log:b", "cache:x", "bare")
	got, err := s.NamespaceStats(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	want := []NamespaceStat{
		{Namespace: "log", Keys: 2, NoExpiry: 2},
		{Namespace: "cache", Keys: 1, NoExpiry: 1},
		{Namespace: "", Keys: 1, NoExpiry: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNamespaceStats_NoExpiryCountsOnlyKeysWithoutTTL(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	setKeys(t, s, "a1", "log:forever")
	if err := s.Set(ctx, "a1", "log:ttl", "v", time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := s.NamespaceStats(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Keys != 2 || got[0].NoExpiry != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestNamespaceStats_SkipsExpiredKeys(t *testing.T) {
	s := mustStore(t)
	ctx := context.Background()
	setKeys(t, s, "a1", "log:live")
	if err := s.Set(ctx, "a1", "log:old", "v", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE kv SET expires_at = datetime('now', '-1 minute') WHERE key = 'log:old'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.NamespaceStats(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Keys != 1 {
		t.Fatalf("expired key counted: %+v", got)
	}
}

func TestNamespaceStats_OtherAgentInvisible(t *testing.T) {
	s := mustStore(t)
	setKeys(t, s, "a1", "log:a")
	setKeys(t, s, "a2", "secret:x", "log:b")
	got, err := s.NamespaceStats(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Namespace != "log" || got[0].Keys != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestNamespaceStats_EmptyAgentIsError(t *testing.T) {
	if _, err := mustStore(t).NamespaceStats(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty agent")
	}
}
