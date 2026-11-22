package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	got := out.String()
	if !strings.Contains(got, "No answer; nothing was written. Pass --yes to run without a prompt.") || hasExpiry(t, dbPath, "log:a") {
		t.Errorf("EOF at the prompt wrote, or did not say how to run without one:\n%s", got)
	}
	if strings.Contains(got, "Cancelled.") {
		t.Errorf("EOF printed Cancelled., which tells a script nothing:\n%s", got)
	}
}

// backdateKey moves a key's updated_at into the past, so its backfilled
// expiry is already over.
func backdateKey(t *testing.T, dbPath, key string, age time.Duration) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	stamp := time.Now().UTC().Add(-age).Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`UPDATE kv SET updated_at = ? WHERE agent_name = 'pamela' AND key = ?`, stamp, key); err != nil {
		t.Fatalf("backdate %s: %v", key, err)
	}
}

func TestKVBackfillTTL_PromptNamesCountWithoutExpiryWarning(t *testing.T) {
	cfgPath, _ := kvBackfillFixture(t, `{ "log:" = "30d" }`)

	var out bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &out, strings.NewReader("n\n"), cfgPath, false, false); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	got := out.String()
	want := "Set expiries on 2 key(s), including any stored without one on purpose? [y/N]: "
	if !strings.Contains(got, want) {
		t.Errorf("prompt missing %q:\n%s", want, got)
	}
	if strings.Contains(got, "disappear") || strings.Contains(got, "cannot be undone") {
		t.Errorf("warned about lost keys with none already expired:\n%s", got)
	}
}

// The warning goes on its own line before the question, so the irreversible
// part is read first and the question stays short.
func TestKVBackfillTTL_PromptWarnsWhenKeysAlreadyExpired(t *testing.T) {
	cfgPath, dbPath := kvBackfillFixture(t, `{ "log:" = "30d" }`)
	backdateKey(t, dbPath, "log:a", 800*time.Hour)
	backdateKey(t, dbPath, "log:b", 800*time.Hour)

	var out bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &out, strings.NewReader("n\n"), cfgPath, false, false); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	want := "\n2 keys are already past their new expiry and will disappear at once. This cannot be undone.\n" +
		"Set expiries on 2 key(s), including any stored without one on purpose? [y/N]: "
	if got := out.String(); !strings.Contains(got, want) {
		t.Errorf("prompt missing %q:\n%s", want, got)
	}
}

func TestKVBackfillTTL_PromptWarnsAboutOneExpiredKeyInTheSingular(t *testing.T) {
	cfgPath, dbPath := kvBackfillFixture(t, `{ "log:" = "30d" }`)
	backdateKey(t, dbPath, "log:a", 800*time.Hour)

	var out bytes.Buffer
	if err := runKVBackfillTTL(context.Background(), &out, strings.NewReader("n\n"), cfgPath, false, false); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	want := "\n1 key is already past its new expiry and will disappear at once. This cannot be undone.\n" +
		"Set expiries on 2 key(s), including any stored without one on purpose? [y/N]: "
	if got := out.String(); !strings.Contains(got, want) {
		t.Errorf("prompt missing %q:\n%s", want, got)
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
