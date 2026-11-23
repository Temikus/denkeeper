package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
)

// chainTag versions the hash preimage. The preimage is frozen: changing a
// field, its order or its encoding needs a new tag and a way to tell rows of
// each version apart, or every existing row fails verification.
const chainTag = "denkeeper.audit.v1"

// chainRow holds the stored columns a row's hash covers.
type chainRow struct {
	ID                                                  int64
	Timestamp, Category, Action, Agent, Summary, Detail string
	Status                                              string
	DurationMs                                          int64
	Source, ConversationID, PrevHash                    string
}

// chainHash is the lowercase hex SHA-256 of the row. Each field is written as
// an 8-byte big-endian length and its raw bytes, so field boundaries cannot
// shift. Timestamp is the exact stored text, never a re-formatted time.
func chainHash(r chainRow) string {
	h := sha256.New()
	fields := [...]string{
		chainTag, strconv.FormatInt(r.ID, 10), r.Timestamp, r.Category, r.Action, r.Agent,
		r.Summary, r.Detail, r.Status, strconv.FormatInt(r.DurationMs, 10), r.Source,
		r.ConversationID, r.PrevHash,
	}
	var n [8]byte
	for _, f := range fields {
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		h.Write(n[:])
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ChainStart says what vouches for the first chained row.
type ChainStart string

const (
	ChainStartEmpty      ChainStart = "empty"      // no rows
	ChainStartNone       ChainStart = "none"       // rows, none hashed
	ChainStartGenesis    ChainStart = "genesis"    // first chained row has an empty prev_hash
	ChainStartCheckpoint ChainStart = "checkpoint" // a prune checkpoint accounts for it
)

// Break kinds.
const (
	BreakHashMismatch = "hash_mismatch"
	BreakLinkMismatch = "link_mismatch"
	BreakUnhashedRow  = "unhashed_row"
	BreakMissingStart = "missing_start"
)

// ChainReport is the result of VerifyChain.
type ChainReport struct {
	Rows            int64 // rows in the table
	LegacyRows      int64 // unhashed rows before the first hashed row
	LegacyFirstID   int64
	LegacyLastID    int64
	Verified        int64 // hashed rows that passed, up to the break
	FirstID         int64 // first hashed row
	Start           ChainStart
	CheckpointID    int64 // Start == checkpoint: the row that vouches for the start
	PrunedThroughID int64
	HeadID          int64 // last verified row
	HeadHash        string
	Break           *ChainBreak // nil when intact
	Unchecked       int64       // rows after the break
}

// ChainBreak is the first row that failed verification.
type ChainBreak struct {
	ID                          int64
	Timestamp, Category, Action string // as stored
	Kind                        string
	Field                       string // "hash" or "prev_hash"
	Expected, Stored            string
	PrevID                      int64 // link_mismatch: the row before
}

// storedRow is one row as read for verification.
type storedRow struct {
	chainRow
	Hash string
}

type checkpointRef struct {
	id, throughID int64
}

// VerifyChain walks audit_events in id order inside one read-only
// transaction and stops at the first row that breaks the chain.
func (s *SQLiteStore) VerifyChain(ctx context.Context) (*ChainReport, error) {
	tx, err := s.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("beginning audit verify transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	rep := &ChainReport{}
	if err := tx.GetContext(ctx, &rep.Rows, `SELECT COUNT(*) FROM audit_events`); err != nil {
		return nil, fmt.Errorf("counting audit events: %w", err)
	}
	if rep.Rows == 0 {
		rep.Start = ChainStartEmpty
		return rep, nil
	}
	hashed, err := hasHashColumn(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !hashed {
		return legacyOnlyReport(ctx, tx, rep)
	}
	starts, err := loadCheckpoints(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := walkChain(ctx, tx, rep, starts); err != nil {
		return nil, err
	}
	if rep.Break == nil && rep.FirstID == 0 {
		rep.Start = ChainStartNone
	}
	return rep, nil
}

func hasHashColumn(ctx context.Context, tx *sqlx.Tx) (bool, error) {
	var n int
	if err := tx.GetContext(ctx, &n, `SELECT COUNT(*) FROM pragma_table_info('audit_events') WHERE name = 'hash'`); err != nil {
		return false, fmt.Errorf("reading audit schema: %w", err)
	}
	return n > 0, nil
}

// legacyOnlyReport covers a file no hashing binary has written to.
func legacyOnlyReport(ctx context.Context, tx *sqlx.Tx, rep *ChainReport) (*ChainReport, error) {
	if err := tx.QueryRowContext(ctx, `SELECT MIN(id), MAX(id) FROM audit_events`).Scan(&rep.LegacyFirstID, &rep.LegacyLastID); err != nil {
		return nil, fmt.Errorf("reading audit id range: %w", err)
	}
	rep.LegacyRows = rep.Rows
	rep.Start = ChainStartNone
	return rep, nil
}

// loadCheckpoints maps each prune checkpoint's pruned_through_hash to the
// checkpoint. Checkpoints with an empty hash or an unreadable detail vouch for
// nothing.
func loadCheckpoints(ctx context.Context, tx *sqlx.Tx) (map[string]checkpointRef, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, detail FROM audit_events WHERE category = ? AND action = ? ORDER BY id`,
		CategoryAudit, ActionPruneCheckpoint)
	if err != nil {
		return nil, fmt.Errorf("reading prune checkpoints: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	out := map[string]checkpointRef{}
	for rows.Next() {
		var id int64
		var detail string
		if err := rows.Scan(&id, &detail); err != nil {
			return nil, fmt.Errorf("scanning prune checkpoint: %w", err)
		}
		var d checkpointPayload
		if json.Unmarshal([]byte(detail), &d) != nil || d.PrunedThroughHash == "" {
			continue
		}
		if _, seen := out[d.PrunedThroughHash]; !seen {
			out[d.PrunedThroughHash] = checkpointRef{id: id, throughID: d.PrunedThroughID}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading prune checkpoints: %w", err)
	}
	return out, nil
}

func walkChain(ctx context.Context, tx *sqlx.Tx, rep *ChainReport, starts map[string]checkpointRef) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, timestamp, category, action, agent, summary, detail, status,
		COALESCE(duration_ms, 0), source, conversation_id, prev_hash, hash FROM audit_events ORDER BY id`)
	if err != nil {
		return fmt.Errorf("reading audit events: %w", err)
	}
	w := &chainWalker{rep: rep, starts: starts}
	for rows.Next() {
		var r storedRow
		if err := rows.Scan(&r.ID, &r.Timestamp, &r.Category, &r.Action, &r.Agent, &r.Summary, &r.Detail,
			&r.Status, &r.DurationMs, &r.Source, &r.ConversationID, &r.PrevHash, &r.Hash); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scanning audit event: %w", err)
		}
		if rep.Break = w.step(r); rep.Break != nil {
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("reading audit events: %w", err)
	}
	_ = rows.Close()
	w.checkCheckpointStart()
	if rep.Break == nil {
		return nil
	}
	if err := tx.GetContext(ctx, &rep.Unchecked, `SELECT COUNT(*) FROM audit_events WHERE id > ?`, rep.Break.ID); err != nil {
		return fmt.Errorf("counting unchecked audit events: %w", err)
	}
	return nil
}

// chainWalker checks rows one at a time, in id order.
type chainWalker struct {
	rep      *ChainReport
	starts   map[string]checkpointRef
	started  bool
	first    storedRow // the first hashed row
	prevID   int64
	prevHash string
}

// checkCheckpointStart runs after the walk. A checkpoint vouches for the
// start only if it is itself in the verified chain (FirstID <= id <= HeadID;
// it may be the first row). Unhashed rows below such a start are a break
// too: a prune deletes the oldest ids first, so none can survive one that
// reached the chain. A break at or before the first row is kept.
func (w *chainWalker) checkCheckpointStart() {
	rep := w.rep
	if rep.Start != ChainStartCheckpoint {
		return
	}
	if rep.Break != nil && rep.Break.ID <= rep.FirstID {
		return
	}
	inChain := rep.Verified > 0 && rep.CheckpointID >= rep.FirstID && rep.CheckpointID <= rep.HeadID
	if inChain && rep.LegacyRows == 0 {
		return
	}
	rep.Break = newBreak(w.first, BreakMissingStart, "prev_hash", "", w.first.PrevHash)
	rep.Start, rep.CheckpointID, rep.PrunedThroughID = "", 0, 0
	rep.Verified, rep.HeadID, rep.HeadHash = 0, 0, ""
}

func (w *chainWalker) step(r storedRow) *ChainBreak {
	if r.Hash == "" {
		if w.started {
			return newBreak(r, BreakUnhashedRow, "hash", "", "")
		}
		w.noteLegacy(r.ID)
		return nil
	}
	if want := chainHash(r.chainRow); want != r.Hash {
		return newBreak(r, BreakHashMismatch, "hash", want, r.Hash)
	}
	if !w.started {
		if b := w.start(r); b != nil {
			return b
		}
	} else if r.PrevHash != w.prevHash {
		b := newBreak(r, BreakLinkMismatch, "prev_hash", w.prevHash, r.PrevHash)
		b.PrevID = w.prevID
		return b
	}
	w.started, w.prevID, w.prevHash = true, r.ID, r.Hash
	w.rep.Verified++
	w.rep.HeadID, w.rep.HeadHash = r.ID, r.Hash
	return nil
}

func (w *chainWalker) noteLegacy(id int64) {
	if w.rep.LegacyRows == 0 {
		w.rep.LegacyFirstID = id
	}
	w.rep.LegacyRows++
	w.rep.LegacyLastID = id
}

// start checks what vouches for the first chained row.
func (w *chainWalker) start(r storedRow) *ChainBreak {
	w.rep.FirstID, w.first = r.ID, r
	if r.PrevHash == "" {
		w.rep.Start = ChainStartGenesis
		return nil
	}
	if cp, ok := w.starts[r.PrevHash]; ok && cp.throughID < r.ID {
		w.rep.Start = ChainStartCheckpoint
		w.rep.CheckpointID, w.rep.PrunedThroughID = cp.id, cp.throughID
		return nil
	}
	return newBreak(r, BreakMissingStart, "prev_hash", "", r.PrevHash)
}

func newBreak(r storedRow, kind, field, expected, stored string) *ChainBreak {
	return &ChainBreak{
		ID: r.ID, Timestamp: r.Timestamp, Category: r.Category, Action: r.Action,
		Kind: kind, Field: field, Expected: expected, Stored: stored,
	}
}
