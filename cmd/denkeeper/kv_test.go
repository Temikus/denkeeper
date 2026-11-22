package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/kv"
)

// kvBackfillFixture writes a config pointing at a temp database seeded with
// two log: keys and one pref: key, none with an expiry. An empty ttlTable
// leaves [kv] default_ttl unset.
func kvBackfillFixture(t *testing.T, ttlTable string) (cfgPath, dbPath string) {
	t.Helper()
	t.Setenv("DENKEEPER_DATA_DIR", "")
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "memory.db")
	body := fmt.Sprintf("data_dir = %q\n\n[memory]\ndb_path = %q\n", dir, dbPath)
	if ttlTable != "" {
		body += "\n[kv]\ndefault_ttl = " + ttlTable + "\n"
	}
	cfgPath = filepath.Join(dir, "denkeeper.toml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := kv.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for _, key := range []string{"log:a", "log:b", "pref:theme"} {
		if err := store.Set(context.Background(), "pamela", key, "v", 0); err != nil {
			t.Fatal(err)
		}
	}
	return cfgPath, dbPath
}

func hasExpiry(t *testing.T, dbPath, key string) bool {
	t.Helper()
	store, err := kv.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	e, ok, err := store.GetEntry(context.Background(), "pamela", key)
	if err != nil || !ok {
		t.Fatalf("GetEntry %s: ok=%v err=%v", key, ok, err)
	}
	return e.ExpiresAt != nil
}

func TestKVBackfillTTL_DryRunPrintsTableAndWritesNothing(t *testing.T) {
	cfgPath, dbPath := kvBackfillFixture(t, `{ "log:" = "30d" }`)

	var out bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &out, strings.NewReader(""), cfgPath, true, false); err != nil {
		t.Fatalf("dry run: %v", err)
	}

	got := out.String()
	for _, want := range []string{"AGENT", "NAMESPACE", "ALREADY EXPIRED", "pamela", "log:", "30d", "Dry run: nothing was written."} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "pref:") || strings.Contains(got, "[y/N]") {
		t.Errorf("dry run listed pref: or prompted:\n%s", got)
	}
	if hasExpiry(t, dbPath, "log:a") {
		t.Error("dry run wrote an expiry")
	}
}

func TestKVBackfillTTL_YesWritesThenSecondRunIsNoop(t *testing.T) {
	cfgPath, dbPath := kvBackfillFixture(t, `{ "log:" = "30d" }`)

	var out bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &out, strings.NewReader(""), cfgPath, false, true); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !strings.Contains(out.String(), "Updated 2 key(s).") || strings.Contains(out.String(), "[y/N]") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
	if !hasExpiry(t, dbPath, "log:a") || hasExpiry(t, dbPath, "pref:theme") {
		t.Error("want log:a to have an expiry and pref:theme none")
	}

	var again bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &again, strings.NewReader(""), cfgPath, false, true); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if strings.TrimSpace(again.String()) != "No keys need a TTL." {
		t.Errorf("second run output = %q, want only %q", again.String(), "No keys need a TTL.")
	}
}

func TestKVBackfillTTL_AnswerNoWritesNothing(t *testing.T) {
	cfgPath, dbPath := kvBackfillFixture(t, `{ "log:" = "30d" }`)

	var out bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &out, strings.NewReader("n\n"), cfgPath, false, false); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "Cancelled.") {
		t.Errorf("want a prompt and Cancelled.:\n%s", out.String())
	}
	if hasExpiry(t, dbPath, "log:a") {
		t.Error("answering n wrote an expiry")
	}
}

// Closed stdin (a pipe, cron) must not count as consent.
func TestKVBackfillTTL_NoAnswerWritesNothing(t *testing.T) {
	cfgPath, dbPath := kvBackfillFixture(t, `{ "log:" = "30d" }`)

	var out bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &out, strings.NewReader(""), cfgPath, false, false); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !strings.Contains(out.String(), "Cancelled.") || hasExpiry(t, dbPath, "log:a") {
		t.Errorf("EOF at the prompt wrote or did not cancel:\n%s", out.String())
	}
}

func TestKVBackfillTTL_NoDefaultTTLIsNotice(t *testing.T) {
	cfgPath, dbPath := kvBackfillFixture(t, "")

	var out bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &out, strings.NewReader(""), cfgPath, false, true); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if !strings.Contains(out.String(), "No [kv] default_ttl") || !strings.Contains(out.String(), cfgPath) {
		t.Errorf("want the notice naming %s:\n%s", cfgPath, out.String())
	}
	if hasExpiry(t, dbPath, "log:a") {
		t.Error("wrote an expiry without default_ttl")
	}
}
