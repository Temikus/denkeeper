package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/Temikus/denkeeper/internal/audit"
)

const (
	reportPath = "/home/me/.denkeeper/data/audit.db"
	hashA      = "5a5e1f79606fee47a1b1555b801509eef93b81e8e561823742f604bc0950abff"
	hashB      = "b8a1405ec74e3ee80506a94cfe513858d794e5c075c8a23813abdab87c5e3601"
)

func renderReport(r *audit.ChainReport) string {
	var b bytes.Buffer
	writeChainReport(&b, reportPath, r)
	return b.String()
}

func assertReport(t *testing.T, r *audit.ChainReport, want string) {
	t.Helper()
	if got := renderReport(r); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteChainReport_Empty(t *testing.T) {
	assertReport(t, &audit.ChainReport{Start: audit.ChainStartEmpty},
		"Audit log: "+reportPath+"\nNo audit events. Nothing to verify.\n")
}

func TestWriteChainReport_OnlyLegacy(t *testing.T) {
	assertReport(t, &audit.ChainReport{Rows: 90210, LegacyRows: 90210, LegacyFirstID: 512, LegacyLastID: 90721, Start: audit.ChainStartNone},
		"Audit log: "+reportPath+"\n"+
			"NOT COVERED: 90210 events (ids 512-90721) have no hash, so none of them can be verified.\n"+
			"Either they were written before the chain existed, or their hashes were removed. Events written by this version of denkeeper serve are chained.\n")
}

func TestWriteChainReport_IntactFromGenesis(t *testing.T) {
	assertReport(t, &audit.ChainReport{Rows: 48211, Verified: 48211, FirstID: 1, HeadID: 48211, HeadHash: hashA, Start: audit.ChainStartGenesis},
		"Audit log: "+reportPath+"\n"+
			"OK: 48211 events verified (ids 1-48211).\n"+
			"Chain start: event 1 is the first chained event.\n"+
			"Head: id 48211 hash "+hashA+"\n")
}

func TestWriteChainReport_IntactWithLegacyPrefix(t *testing.T) {
	assertReport(t, &audit.ChainReport{Rows: 91414, LegacyRows: 90210, LegacyFirstID: 512, LegacyLastID: 90721,
		Verified: 1204, FirstID: 90722, HeadID: 91925, HeadHash: hashA, Start: audit.ChainStartGenesis},
		"Audit log: "+reportPath+"\n"+
			"OK: 1204 events verified (ids 90722-91925).\n"+
			"Not covered: 90210 older events (ids 512-90721) were written before the chain existed. They age out with [audit] retention_days.\n"+
			"Chain start: event 90722 is the first chained event.\n"+
			"Head: id 91925 hash "+hashA+"\n")
}

func TestWriteChainReport_IntactFromCheckpoint(t *testing.T) {
	assertReport(t, &audit.ChainReport{Rows: 31870, Verified: 31870, FirstID: 16342, HeadID: 48211, HeadHash: hashA,
		Start: audit.ChainStartCheckpoint, CheckpointID: 47003, PrunedThroughID: 16341},
		"Audit log: "+reportPath+"\n"+
			"OK: 31870 events verified (ids 16342-48211).\n"+
			"Chain start: prune checkpoint 47003 accounts for the events up to id 16341 that retention removed.\n"+
			"Head: id 48211 hash "+hashA+"\n")
}

// brokenReport has 4075 verified rows before a break at 20417 and 27794 after it.
func brokenReport(b audit.ChainBreak) *audit.ChainReport {
	b.ID, b.Timestamp, b.Category, b.Action = 20417, "2026-11-20T08:14:03.512Z", "tool_call", "execute"
	return &audit.ChainReport{Rows: 31870, Verified: 4075, FirstID: 16342, HeadID: 20416, Break: &b, Unchecked: 27794}
}

const brokenHeader = "Audit log: " + reportPath + "\nBROKEN at event 20417 (2026-11-20T08:14:03.512Z tool_call/execute): "
const brokenFooter = "Verified before the break: 4075 events (ids 16342-20416).\nNot checked: 27794 events after id 20417.\n"

func TestWriteChainReport_HashMismatch(t *testing.T) {
	assertReport(t, brokenReport(audit.ChainBreak{Kind: audit.BreakHashMismatch, Field: "hash", Expected: hashA, Stored: hashB}),
		brokenHeader+"the event does not match its stored hash. It was changed after it was written.\n"+
			"  expected hash: "+hashA+"\n"+
			"  stored hash:   "+hashB+"\n"+brokenFooter)
}

func TestWriteChainReport_LinkMismatch(t *testing.T) {
	assertReport(t, brokenReport(audit.ChainBreak{Kind: audit.BreakLinkMismatch, Field: "prev_hash", Expected: hashA, Stored: hashB, PrevID: 20415}),
		brokenHeader+"its prev_hash is not the hash of event 20415 before it. Events were removed, added or reordered here.\n"+
			"  expected prev_hash: "+hashA+"\n"+
			"  stored prev_hash:   "+hashB+"\n"+brokenFooter)
}

func TestWriteChainReport_UnhashedRow(t *testing.T) {
	assertReport(t, brokenReport(audit.ChainBreak{Kind: audit.BreakUnhashedRow, Field: "hash"}),
		brokenHeader+"the event has no hash but follows chained events. It was written by an older version, written outside denkeeper, or its hash was removed.\n"+
			brokenFooter)
}

func TestWriteChainReport_MissingStart(t *testing.T) {
	assertReport(t, brokenReport(audit.ChainBreak{Kind: audit.BreakMissingStart, Field: "prev_hash", Stored: hashB}),
		brokenHeader+"it is the first chained event but points at an earlier one, and no prune checkpoint accounts for the gap. Earlier events were removed outside retention.\n"+
			"  expected prev_hash: (empty) or a prune checkpoint's pruned_through_hash\n"+
			"  stored prev_hash:   "+hashB+"\n"+brokenFooter)
}

func TestWriteChainReport_BreakWithNothingAround(t *testing.T) {
	b := audit.ChainBreak{ID: 7, Timestamp: "2026-11-20T08:14:03Z", Category: "audit", Action: "prune_checkpoint",
		Kind: audit.BreakHashMismatch, Field: "hash", Expected: hashA, Stored: ""}
	assertReport(t, &audit.ChainReport{Rows: 3, LegacyRows: 2, LegacyFirstID: 5, LegacyLastID: 6, FirstID: 7, Break: &b},
		"Audit log: "+reportPath+"\n"+
			"BROKEN at event 7 (2026-11-20T08:14:03Z audit/prune_checkpoint): the event does not match its stored hash. It was changed after it was written.\n"+
			"  expected hash: "+hashA+"\n"+
			"  stored hash:   (empty)\n"+
			"No events verified before the break.\n"+
			"Not covered: 2 older events (ids 5-6) were written before the chain existed. They age out with [audit] retention_days.\n")
}

// --- runAuditVerify against a real store ---

func execRaw(path, q string) error {
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck
	_, err = db.Exec(q)
	return err
}

// auditFixture writes a config whose memory db sits in a temp dir, and an
// audit.db with three chained events next to it.
func auditFixture(t *testing.T) (cfgPath, auditPath string) {
	t.Helper()
	t.Setenv("DENKEEPER_MEMORY_DB_PATH", "")
	t.Setenv("DENKEEPER_DATA_DIR", "")
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "denkeeper.toml")
	memPath := filepath.Join(dir, "data", "memory.db")
	if err := os.WriteFile(cfgPath, []byte("[memory]\ndb_path = \""+memPath+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	auditPath = filepath.Join(dir, "data", "audit.db")
	store, err := audit.NewSQLiteStore(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close() //nolint:errcheck
	var events []audit.Event
	for i := range 3 {
		events = append(events, audit.Event{Timestamp: time.Now().UTC(), Category: audit.CategoryToolCall,
			Action: "execute", Summary: "e", Status: audit.StatusOK, DurationMs: int64(i)})
	}
	if err := store.InsertBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	return cfgPath, auditPath
}

func TestRunAuditVerify_Intact(t *testing.T) {
	cfgPath, _ := auditFixture(t)
	var out, progress bytes.Buffer
	if err := runAuditVerify(context.Background(), &out, &progress, cfgPath); err != nil {
		t.Fatalf("runAuditVerify: %v", err)
	}
	if !strings.Contains(out.String(), "OK: 3 events verified (ids 1-3).") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestRunAuditVerify_EditedRow(t *testing.T) {
	cfgPath, auditPath := auditFixture(t)
	// Tamper through a plain SQL handle, as someone with file access would.
	if err := execRaw(auditPath, `UPDATE audit_events SET status = 'denied' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}

	var out, progress bytes.Buffer
	err := runAuditVerify(context.Background(), &out, &progress, cfgPath)
	if err == nil || err.Error() != "audit log failed verification at event 2" {
		t.Fatalf("err = %v", err)
	}
	if exitCode(err) != 1 {
		t.Fatalf("exit code %d, want 1", exitCode(err))
	}
	if !strings.Contains(out.String(), "BROKEN at event 2 (") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestRunAuditVerify_OnlyLegacyExitsTwo(t *testing.T) {
	cfgPath, auditPath := auditFixture(t)
	if err := execRaw(auditPath, `UPDATE audit_events SET hash = '', prev_hash = ''`); err != nil {
		t.Fatal(err)
	}
	var out, progress bytes.Buffer
	err := runAuditVerify(context.Background(), &out, &progress, cfgPath)
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("err = %v (exit %d), want exit 2", err, exitCode(err))
	}
	if !strings.Contains(out.String(), "NOT COVERED: 3 events") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestRunAuditVerify_NoAuditLog(t *testing.T) {
	cfgPath, auditPath := auditFixture(t)
	if err := os.Remove(auditPath); err != nil {
		t.Fatal(err)
	}
	var out, progress bytes.Buffer
	err := runAuditVerify(context.Background(), &out, &progress, cfgPath)
	if err == nil || !strings.Contains(err.Error(), auditPath) || !strings.Contains(err.Error(), "[audit] enabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAuditVerify_ProgressGoesToProgressWriter(t *testing.T) {
	cfgPath, auditPath := auditFixture(t)
	var out, progress bytes.Buffer
	if err := runAuditVerify(context.Background(), &out, &progress, cfgPath); err != nil {
		t.Fatal(err)
	}
	if progress.String() != "Verifying audit log "+auditPath+"...\n" {
		t.Fatalf("progress = %q", progress.String())
	}
	if strings.Contains(out.String(), "Verifying") {
		t.Fatal("progress line written to stdout")
	}
}

func TestNewAuditCmd_VerifySubcommand(t *testing.T) {
	cmd := newAuditCmd()
	verify, _, err := cmd.Find([]string{"verify"})
	if err != nil || verify == nil || verify.Name() != "verify" {
		t.Fatalf("verify subcommand missing: %v", err)
	}
	if !verify.SilenceUsage {
		t.Error("verify does not silence usage")
	}
	if cmd.PersistentFlags().Lookup("config") == nil {
		t.Error("no --config flag")
	}
}

func TestExitCode_DefaultsToOne(t *testing.T) {
	if exitCode(errors.New("plain")) != 1 {
		t.Fatal("a plain error must exit 1")
	}
}
