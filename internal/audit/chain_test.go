package audit

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// --- helpers ---

func newChainStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newChainFileStore(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

var chainBase = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func chainEvent(i int) Event {
	return Event{
		Timestamp:      chainBase.Add(time.Duration(i) * time.Second),
		Category:       CategoryToolCall,
		Action:         "execute",
		Agent:          "pamela",
		Summary:        fmt.Sprintf("event %d", i),
		Detail:         fmt.Sprintf(`{"n":%d}`, i),
		Status:         StatusOK,
		DurationMs:     int64(10 * i),
		Source:         "engine",
		ConversationID: "telegram:42",
	}
}

// insertChained inserts n events numbered from start in one batch.
func insertChained(t *testing.T, s *SQLiteStore, start, n int) {
	t.Helper()
	events := make([]Event, n)
	for i := range n {
		events[i] = chainEvent(start + i)
	}
	if err := s.InsertBatch(context.Background(), events); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
}

func rawExec(t *testing.T, s *SQLiteStore, q string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		t.Fatalf("raw %q: %v", q, err)
	}
}

func insertLegacy(t *testing.T, s *SQLiteStore, n int) {
	t.Helper()
	for i := range n {
		rawExec(t, s, `INSERT INTO audit_events (timestamp, category, action, summary) VALUES (?, 'tool_call', 'execute', ?)`,
			chainBase.Add(time.Duration(i)*time.Second).Format(time.RFC3339Nano), fmt.Sprintf("legacy %d", i))
	}
}

func storedHash(t *testing.T, s *SQLiteStore, id int64) (prev, hash string) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT prev_hash, hash FROM audit_events WHERE id = ?`, id).Scan(&prev, &hash); err != nil {
		t.Fatalf("reading hash of %d: %v", id, err)
	}
	return prev, hash
}

func verifyChain(t *testing.T, s *SQLiteStore) *ChainReport {
	t.Helper()
	rep, err := s.VerifyChain(context.Background())
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	return rep
}

func requireIntact(t *testing.T, rep *ChainReport) {
	t.Helper()
	if rep.Break != nil {
		t.Fatalf("chain broken at %d (%s): expected %q, stored %q", rep.Break.ID, rep.Break.Kind, rep.Break.Expected, rep.Break.Stored)
	}
}

func requireBreak(t *testing.T, rep *ChainReport, id int64, kind string) *ChainBreak {
	t.Helper()
	if rep.Break == nil {
		t.Fatalf("expected %s at %d, chain intact (verified %d)", kind, id, rep.Verified)
	}
	if rep.Break.ID != id || rep.Break.Kind != kind {
		t.Fatalf("break = %s at %d, want %s at %d", rep.Break.Kind, rep.Break.ID, kind, id)
	}
	return rep.Break
}

// --- hash ---

func TestChainHash_GoldenVectors(t *testing.T) {
	r1 := chainRow{
		ID: 1, Timestamp: "2026-11-23T10:00:00.123456789Z", Category: "tool_call", Action: "execute",
		Agent: "pamela", Summary: "Executed tool weather_lookup", Detail: `{"tool":"weather_lookup"}`,
		Status: "ok", DurationMs: 150, Source: "engine", ConversationID: "telegram:42",
	}
	const want1 = "5a5e1f79606fee47a1b1555b801509eef93b81e8e561823742f604bc0950abff"
	if got := chainHash(r1); got != want1 {
		t.Fatalf("row 1 = %s, want %s", got, want1)
	}
	r2 := chainRow{
		ID: 2, Timestamp: "2026-11-23T10:00:01Z", Category: "audit", Action: "dropped_events",
		Status: "error", PrevHash: want1,
	}
	const want2 = "b8a1405ec74e3ee80506a94cfe513858d794e5c075c8a23813abdab87c5e3601"
	if got := chainHash(r2); got != want2 {
		t.Fatalf("row 2 = %s, want %s", got, want2)
	}
}

func TestChainHash_FieldBoundariesMatter(t *testing.T) {
	a := chainRow{ID: 1, Agent: "ab", Summary: "c"}
	b := chainRow{ID: 1, Agent: "a", Summary: "bc"}
	if chainHash(a) == chainHash(b) {
		t.Fatal("moving a byte between fields kept the hash")
	}
}

// --- insert ---

func TestInsertBatch_ChainsEachRow(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 3)

	prev1, hash1 := storedHash(t, s, 1)
	prev2, hash2 := storedHash(t, s, 2)
	prev3, _ := storedHash(t, s, 3)
	if prev1 != "" || prev2 != hash1 || prev3 != hash2 {
		t.Fatalf("links wrong: %q %q/%q %q/%q", prev1, prev2, hash1, prev3, hash2)
	}
	if len(hash1) != 64 {
		t.Fatalf("hash %q is not 64 hex chars", hash1)
	}
	rep := verifyChain(t, s)
	requireIntact(t, rep)
	if rep.Start != ChainStartGenesis || rep.Verified != 3 || rep.FirstID != 1 || rep.HeadID != 3 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestInsertBatch_SecondBatchLinksToHead(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 2)
	insertChained(t, s, 3, 2)
	_, hash2 := storedHash(t, s, 2)
	if prev3, _ := storedHash(t, s, 3); prev3 != hash2 {
		t.Fatalf("second batch prev_hash %q, want %q", prev3, hash2)
	}
	requireIntact(t, verifyChain(t, s))
}

func TestInsertBatch_LinksAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	s1, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	insertChained(t, s1, 1, 2)
	_, hash2 := storedHash(t, s1, 2)
	_ = s1.Close()

	s2 := newChainFileStore(t, path)
	insertChained(t, s2, 3, 1)
	if prev3, _ := storedHash(t, s2, 3); prev3 != hash2 {
		t.Fatalf("after reopen prev_hash %q, want %q", prev3, hash2)
	}
	requireIntact(t, verifyChain(t, s2))
}

func TestInsert_SingleEventIsChained(t *testing.T) {
	s := newChainStore(t)
	if err := s.Insert(context.Background(), chainEvent(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(context.Background(), chainEvent(2)); err != nil {
		t.Fatal(err)
	}
	_, hash1 := storedHash(t, s, 1)
	if prev2, hash2 := storedHash(t, s, 2); prev2 != hash1 || hash2 == "" {
		t.Fatalf("Insert not chained: prev %q hash %q", prev2, hash2)
	}
	requireIntact(t, verifyChain(t, s))
}

// --- verify, intact ---

func TestVerifyChain_Empty(t *testing.T) {
	rep := verifyChain(t, newChainStore(t))
	requireIntact(t, rep)
	if rep.Start != ChainStartEmpty || rep.Rows != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyChain_LegacyPrefixThenChain(t *testing.T) {
	s := newChainStore(t)
	insertLegacy(t, s, 3)
	insertChained(t, s, 4, 2)
	rep := verifyChain(t, s)
	requireIntact(t, rep)
	if rep.LegacyRows != 3 || rep.LegacyFirstID != 1 || rep.LegacyLastID != 3 {
		t.Fatalf("legacy = %d (%d-%d)", rep.LegacyRows, rep.LegacyFirstID, rep.LegacyLastID)
	}
	if rep.FirstID != 4 || rep.Start != ChainStartGenesis || rep.Verified != 2 || rep.Rows != 5 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyChain_OnlyLegacyRows(t *testing.T) {
	s := newChainStore(t)
	insertLegacy(t, s, 2)
	rep := verifyChain(t, s)
	requireIntact(t, rep)
	if rep.Start != ChainStartNone || rep.LegacyRows != 2 || rep.Verified != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyChain_ArbitraryDetailBytes(t *testing.T) {
	s := newChainStore(t)
	e := chainEvent(1)
	e.Detail = "bad \xff\xfe utf8, nul \x00 here, emoji 🦊"
	if err := s.InsertBatch(context.Background(), []Event{e, chainEvent(2)}); err != nil {
		t.Fatal(err)
	}
	requireIntact(t, verifyChain(t, s))
}

func TestVerifyChain_NonUTCTimestamp(t *testing.T) {
	s := newChainStore(t)
	e := chainEvent(1)
	e.Timestamp = time.Date(2026, 11, 23, 21, 0, 0, 5, time.FixedZone("AEDT", 11*3600))
	if err := s.InsertBatch(context.Background(), []Event{e}); err != nil {
		t.Fatal(err)
	}
	requireIntact(t, verifyChain(t, s))
}

// Inserts never write NULL, so a NULL duration_ms is an edit, even from 0.
func TestVerifyChain_NullDurationIsHashMismatch(t *testing.T) {
	s := newChainStore(t)
	e := chainEvent(1)
	e.DurationMs = 0
	if err := s.InsertBatch(context.Background(), []Event{e, chainEvent(2)}); err != nil {
		t.Fatal(err)
	}
	rawExec(t, s, `UPDATE audit_events SET duration_ms = NULL WHERE id = 1`)
	requireBreak(t, verifyChain(t, s), 1, BreakHashMismatch)
}

func TestChainHash_NullDurationDiffersFromZero(t *testing.T) {
	r := chainRow{ID: 1, Timestamp: "t", DurationMs: 0}
	null := r
	null.DurationNull = true
	if chainHash(r) == chainHash(null) {
		t.Fatal("a NULL duration_ms hashes the same as 0")
	}
}

// Removing the newest rows is a documented limit: nothing later disagrees.
func TestVerifyChain_DeletedTailStillVerifies(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 4)
	rawExec(t, s, `DELETE FROM audit_events WHERE id = 4`)
	rep := verifyChain(t, s)
	requireIntact(t, rep)
	if rep.HeadID != 3 {
		t.Fatalf("HeadID = %d, want 3", rep.HeadID)
	}
}

// --- verify, breaks ---

// tamperColumn chains five rows, edits row 3 and checks verify reports a
// hash mismatch there.
func tamperColumn(t *testing.T, col string, value any) {
	t.Helper()
	s := newChainStore(t)
	insertChained(t, s, 1, 5)
	_, before := storedHash(t, s, 3)
	rawExec(t, s, "UPDATE audit_events SET "+col+" = ? WHERE id = 3", value)

	rep := verifyChain(t, s)
	b := requireBreak(t, rep, 3, BreakHashMismatch)
	if b.Field != "hash" || b.Stored != before || b.Expected == before || len(b.Expected) != 64 {
		t.Fatalf("break = %+v (hash before edit %s)", b, before)
	}
	if rep.Verified != 2 || rep.Unchecked != 2 {
		t.Fatalf("verified %d unchecked %d, want 2 and 2", rep.Verified, rep.Unchecked)
	}
}

func TestVerifyChain_EditedTimestamp(t *testing.T) {
	tamperColumn(t, "timestamp", "2020-01-01T00:00:00Z")
}

func TestVerifyChain_EditedCategory(t *testing.T) { tamperColumn(t, "category", "config") }

func TestVerifyChain_EditedAction(t *testing.T) { tamperColumn(t, "action", "delete") }

func TestVerifyChain_EditedAgent(t *testing.T) { tamperColumn(t, "agent", "mallory") }

func TestVerifyChain_EditedSummary(t *testing.T) { tamperColumn(t, "summary", "nothing happened") }

func TestVerifyChain_EditedDetail(t *testing.T) { tamperColumn(t, "detail", `{"n": 3}`) }

func TestVerifyChain_EditedStatus(t *testing.T) { tamperColumn(t, "status", StatusDenied) }

func TestVerifyChain_EditedDuration(t *testing.T) { tamperColumn(t, "duration_ms", 999) }

func TestVerifyChain_EditedSource(t *testing.T) { tamperColumn(t, "source", "operator") }

func TestVerifyChain_EditedConversationID(t *testing.T) {
	tamperColumn(t, "conversation_id", "telegram:43")
}

func TestVerifyChain_EditedHash(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 3)
	forged := "0000000000000000000000000000000000000000000000000000000000000000"
	rawExec(t, s, `UPDATE audit_events SET hash = ? WHERE id = 2`, forged)
	b := requireBreak(t, verifyChain(t, s), 2, BreakHashMismatch)
	if b.Stored != forged || b.Expected == forged {
		t.Fatalf("break = %+v", b)
	}
}

func TestVerifyChain_DeletedMiddleRow(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 4)
	_, hash1 := storedHash(t, s, 1)
	prev3, _ := storedHash(t, s, 3)
	rawExec(t, s, `DELETE FROM audit_events WHERE id = 2`)

	rep := verifyChain(t, s)
	b := requireBreak(t, rep, 3, BreakLinkMismatch)
	if b.PrevID != 1 || b.Field != "prev_hash" || b.Expected != hash1 || b.Stored != prev3 {
		t.Fatalf("break = %+v", b)
	}
	if rep.Verified != 1 || rep.Unchecked != 1 {
		t.Fatalf("verified %d unchecked %d", rep.Verified, rep.Unchecked)
	}
}

func TestVerifyChain_DeletedPrefixWithoutCheckpoint(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 4)
	prev3, _ := storedHash(t, s, 3)
	rawExec(t, s, `DELETE FROM audit_events WHERE id < 3`)

	rep := verifyChain(t, s)
	b := requireBreak(t, rep, 3, BreakMissingStart)
	if b.Expected != "" || b.Stored != prev3 || rep.Verified != 0 || rep.Unchecked != 1 {
		t.Fatalf("break = %+v, report = %+v", b, rep)
	}
}

func TestVerifyChain_BlankedLeadingHashes(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 4)
	rawExec(t, s, `UPDATE audit_events SET hash = '' WHERE id <= 2`)

	rep := verifyChain(t, s)
	requireBreak(t, rep, 3, BreakMissingStart)
	if rep.LegacyRows != 2 || rep.Verified != 0 || rep.Unchecked != 1 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyChain_BlankedMiddleHash(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 4)
	rawExec(t, s, `UPDATE audit_events SET hash = '' WHERE id = 3`)

	rep := verifyChain(t, s)
	b := requireBreak(t, rep, 3, BreakUnhashedRow)
	if b.Expected != "" || b.Stored != "" || rep.Verified != 2 || rep.Unchecked != 1 {
		t.Fatalf("break = %+v, report = %+v", b, rep)
	}
}

func TestVerifyChain_UnhashedRowAfterChain(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 2)
	insertLegacy(t, s, 1)

	rep := verifyChain(t, s)
	requireBreak(t, rep, 3, BreakUnhashedRow)
	if rep.Verified != 2 || rep.Unchecked != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyChain_EditedCheckpointDetail(t *testing.T) {
	s := newChainStore(t)
	old := chainEvent(1)
	old.Timestamp = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.InsertBatch(context.Background(), []Event{old, chainEvent(2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneBefore(context.Background(), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	rawExec(t, s, `UPDATE audit_events SET detail = replace(detail, '"pruned_rows":1', '"pruned_rows":0') WHERE action = ?`, ActionPruneCheckpoint)

	// The walk's own, more specific break is reported, not missing_start.
	rep := requireBreakAt(t, verifyChain(t, s), 3, BreakHashMismatch)
	if rep.Verified != 1 || rep.Unchecked != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

// An older binary writes unhashed events after chained ones. Once retention
// has pruned the chained events before them, they read as a not-covered
// prefix and the chain verifies again.
func TestVerifyChain_RollbackBreakHealsAfterRetention(t *testing.T) {
	s := newChainStore(t)
	insertAt(t, s, eventAt(pruneOld, "old 1"), eventAt(pruneOld, "old 2"))
	now := time.Now().UTC()
	for range 2 {
		rawExec(t, s, `INSERT INTO audit_events (timestamp, category, action, summary) VALUES (?, 'tool_call', 'execute', 'old binary')`,
			now.Format(time.RFC3339Nano))
	}
	insertAt(t, s, eventAt(now, "new binary again"))
	requireBreak(t, verifyChain(t, s), 3, BreakUnhashedRow)

	prune(t, s, pruneCutoff)
	rep := verifyChain(t, s)
	requireIntact(t, rep)
	if rep.Start != ChainStartGenesis || rep.LegacyRows != 2 || rep.FirstID != 5 {
		t.Fatalf("report = %+v", rep)
	}
}

func requireBreakAt(t *testing.T, rep *ChainReport, id int64, kind string) *ChainReport {
	t.Helper()
	requireBreak(t, rep, id, kind)
	return rep
}

// A forged checkpoint that is not itself in the chain must not vouch for a
// deleted prefix.
func TestVerifyChain_ForgedHiddenCheckpoint(t *testing.T) {
	s := newChainStore(t)
	insertChained(t, s, 1, 4)
	_, hash2 := storedHash(t, s, 2)
	rawExec(t, s, `DELETE FROM audit_events WHERE id <= 2`)
	detail := fmt.Sprintf(`{"pruned_rows":2,"pruned_through_id":2,"pruned_through_hash":%q,"cutoff":"x"}`, hash2)
	rawExec(t, s, `INSERT INTO audit_events (id, timestamp, category, action, summary, detail) VALUES (1, ?, ?, ?, 'forged', ?)`,
		chainBase.Format(time.RFC3339Nano), CategoryAudit, ActionPruneCheckpoint, detail)

	rep := requireBreakAt(t, verifyChain(t, s), 3, BreakMissingStart)
	if rep.Verified != 0 || rep.Unchecked != 1 || rep.Start == ChainStartCheckpoint {
		t.Fatalf("report = %+v", rep)
	}
}

// A real prune deletes the oldest ids first, so unhashed rows below a chain
// that starts at a checkpoint were put there afterwards.
func TestVerifyChain_LegacyRowsWithCheckpointStart(t *testing.T) {
	s := newChainStore(t)
	old := chainEvent(1)
	old.Timestamp = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.InsertBatch(context.Background(), []Event{old, chainEvent(2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneBefore(context.Background(), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	requireIntact(t, verifyChain(t, s))

	rawExec(t, s, `INSERT INTO audit_events (id, timestamp, category, action, summary) VALUES (1, ?, 'tool_call', 'execute', 'planted')`,
		chainBase.Format(time.RFC3339Nano))
	rep := requireBreakAt(t, verifyChain(t, s), 2, BreakMissingStart)
	if rep.LegacyRows != 1 {
		t.Fatalf("report = %+v", rep)
	}
}
