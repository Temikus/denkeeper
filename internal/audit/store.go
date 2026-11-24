package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const auditSchema = `
CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL,
    category TEXT NOT NULL,
    action TEXT NOT NULL,
    agent TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ok',
    duration_ms INTEGER DEFAULT 0,
    source TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_events(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_category ON audit_events(category, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_agent ON audit_events(agent, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_conversation ON audit_events(conversation_id, timestamp DESC);
-- reviewed_up_to_ns is Unix nanoseconds: RFC3339Nano text does not sort in
-- time order within a second, and the mark must never move backwards.
CREATE TABLE IF NOT EXISTS decider_review_marks (
    agent TEXT NOT NULL,
    decider TEXT NOT NULL,
    reviewed_up_to_ns INTEGER NOT NULL,
    reviewed_by TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (agent, decider)
);
`

// auditMigrations add the hash-chain columns. A constant default does not
// rewrite rows, so this is safe on a database another process is using.
var auditMigrations = []string{
	`ALTER TABLE audit_events ADD COLUMN prev_hash TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE audit_events ADD COLUMN hash TEXT NOT NULL DEFAULT ''`,
}

func initAuditDB(db *sqlx.DB) error {
	if _, err := db.Exec(auditSchema); err != nil {
		return fmt.Errorf("initializing audit schema: %w", err)
	}
	for _, m := range auditMigrations {
		if _, err := db.Exec(m); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("migrating audit schema: %w", err)
		}
	}
	return nil
}

func isDuplicateColumn(err error) bool {
	return strings.Contains(err.Error(), "duplicate column name")
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sqlx.DB
	// chainMu orders this process's chain writers (the flush goroutine and
	// the retention worker). _txlock=immediate orders other processes.
	chainMu sync.Mutex
}

// Compile-time check.
var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens (or creates) the audit database at dbPath.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating audit database directory: %w", err)
	}

	// _txlock=immediate: a write transaction takes the write lock before it
	// reads the chain head, so two processes cannot both extend the same head.
	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening audit database: %w", err)
	}

	if err := initAuditDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteStore{db: db}, nil
}

// OpenSQLiteStoreReadOnly opens an existing audit database for reading only:
// no schema setup, no migration, and a missing file is an error.
func OpenSQLiteStoreReadOnly(dbPath string) (*SQLiteStore, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("opening audit database read-only: %w", err)
	}
	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening audit database read-only: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opening audit database read-only: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// NewInMemoryStore creates an in-memory audit store (for testing).
func NewInMemoryStore() (*SQLiteStore, error) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	// Each pooled connection to ":memory:" is a separate, empty database,
	// so a second connection would see no audit_events table.
	db.SetMaxOpenConns(1)
	if err := initAuditDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Insert persists a single audit event.
func (s *SQLiteStore) Insert(ctx context.Context, event Event) error {
	return s.InsertBatch(ctx, []Event{event})
}

// InsertBatch persists multiple audit events in a single transaction, each
// chained to the row before it.
func (s *SQLiteStore) InsertBatch(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}

	s.chainMu.Lock()
	defer s.chainMu.Unlock()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning audit batch transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	_, headHash, _, err := readChainHead(ctx, tx)
	if err != nil {
		return err
	}
	if err := appendChained(ctx, tx, headHash, events); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing audit batch: %w", err)
	}
	return nil
}

// readChainHead returns the newest row's id and hash. ok is false for an
// empty table. Read on every append: nothing caches the head.
func readChainHead(ctx context.Context, tx *sqlx.Tx) (id int64, hash string, ok bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT id, hash FROM audit_events ORDER BY id DESC LIMIT 1`).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("reading audit chain head: %w", err)
	}
	return id, hash, true, nil
}

// appendChained inserts events after a head whose hash is prevHash. Every
// append goes through here, under chainMu, in a write transaction.
func appendChained(ctx context.Context, tx *sqlx.Tx, prevHash string, events []Event) error {
	ins, err := tx.PrepareContext(ctx, `INSERT INTO audit_events
		(timestamp, category, action, agent, summary, detail, status, duration_ms, source, conversation_id, prev_hash, hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')`)
	if err != nil {
		return fmt.Errorf("preparing audit insert: %w", err)
	}
	defer ins.Close() //nolint:errcheck
	upd, err := tx.PrepareContext(ctx, `UPDATE audit_events SET hash = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("preparing audit hash update: %w", err)
	}
	defer upd.Close() //nolint:errcheck

	for _, event := range events {
		ts := event.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		// Format once: the stored text and the hashed text must be the same bytes.
		r := chainRow{
			Timestamp: ts.Format(time.RFC3339Nano), Category: event.Category, Action: event.Action,
			Agent: event.Agent, Summary: event.Summary, Detail: event.Detail, Status: event.Status,
			DurationMs: event.DurationMs, Source: event.Source, ConversationID: event.ConversationID,
			PrevHash: prevHash,
		}
		res, err := ins.ExecContext(ctx, r.Timestamp, r.Category, r.Action, r.Agent, r.Summary,
			r.Detail, r.Status, r.DurationMs, r.Source, r.ConversationID, r.PrevHash)
		if err != nil {
			return fmt.Errorf("inserting audit event: %w", err)
		}
		if r.ID, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("reading audit event id: %w", err)
		}
		hash := chainHash(r)
		if _, err := upd.ExecContext(ctx, hash, r.ID); err != nil {
			return fmt.Errorf("storing audit event hash: %w", err)
		}
		prevHash = hash
	}
	return nil
}

// inClause renders a set-membership condition for col, or ("", nil) when vals
// is empty so the caller can drop the filter entirely. The placeholders are
// generated from len(vals) — the values themselves are always bound, never
// interpolated.
func inClause(col string, vals []string) (string, []any) {
	if len(vals) == 0 {
		return "", nil
	}
	args := make([]any, len(vals))
	for i, v := range vals {
		args[i] = v
	}
	return col + " IN (?" + strings.Repeat(",?", len(vals)-1) + ")", args
}

// buildWhereClause constructs WHERE conditions from ListOpts.
func buildWhereClause(opts ListOpts) (string, []any) {
	var where []string
	var args []any

	if cond, vals := inClause("category", opts.Categories); cond != "" {
		where = append(where, cond)
		args = append(args, vals...)
	}
	if opts.Agent != "" {
		where = append(where, "agent = ?")
		args = append(args, opts.Agent)
	}
	if cond, vals := inClause("status", opts.Statuses); cond != "" {
		where = append(where, cond)
		args = append(args, vals...)
	}
	if opts.Source != "" {
		where = append(where, "source = ?")
		args = append(args, opts.Source)
	}
	if cond, vals := inClause("source", opts.ExcludeSources); cond != "" {
		where = append(where, "NOT "+cond)
		args = append(args, vals...)
	}
	if opts.Search != "" {
		where = append(where, "summary LIKE ?")
		args = append(args, "%"+opts.Search+"%")
	}
	if opts.Since != nil {
		where = append(where, "timestamp >= ?")
		args = append(args, opts.Since.Format(time.RFC3339Nano))
	}
	if opts.Until != nil {
		where = append(where, "timestamp <= ?")
		args = append(args, opts.Until.Format(time.RFC3339Nano))
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	return clause, args
}

// List queries audit events with filtering and pagination.
func (s *SQLiteStore) List(ctx context.Context, opts ListOpts) ([]Event, int, error) {
	whereClause, args := buildWhereClause(opts)

	// Count total.
	var total int
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*) FROM audit_events"+whereClause, args...); err != nil {
		return nil, 0, fmt.Errorf("counting audit events: %w", err)
	}

	// Apply defaults.
	limit := opts.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	query := "SELECT id, timestamp, category, action, agent, summary, detail, status, duration_ms, source, conversation_id FROM audit_events" +
		whereClause + " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	args = append(args, limit, opts.Offset)

	rows, err := s.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("querying audit events: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var events []Event
	for rows.Next() {
		var e Event
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.Category, &e.Action, &e.Agent,
			&e.Summary, &e.Detail, &e.Status, &e.DurationMs, &e.Source, &e.ConversationID); err != nil {
			return nil, 0, fmt.Errorf("scanning audit event: %w", err)
		}
		e.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		e.Detail = truncateDetail(e.Detail, opts.DetailMaxChars)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating audit events: %w", err)
	}

	return events, total, nil
}

// Stats returns aggregate counts for the audit dashboard.
func (s *SQLiteStore) Stats(ctx context.Context, opts StatsOpts) (*Stats, error) {
	st := &Stats{
		ByCategory: make(map[string]int),
		ByStatus:   make(map[string]int),
	}

	// Reuse the list filter builder so exclusion semantics can never drift
	// between the list and the counts shown above it.
	whereClause, args := buildWhereClause(ListOpts{
		Since:          opts.Since,
		ExcludeSources: opts.ExcludeSources,
	})

	// Total.
	var total int
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*) FROM audit_events"+whereClause, args...); err != nil {
		return nil, fmt.Errorf("counting total audit events: %w", err)
	}
	st.Total = total

	// By category.
	type catCount struct {
		Category string `db:"category"`
		Count    int    `db:"cnt"`
	}
	var cats []catCount
	if err := s.db.SelectContext(ctx, &cats,
		"SELECT category, COUNT(*) as cnt FROM audit_events"+whereClause+" GROUP BY category", args...); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("counting audit events by category: %w", err)
	}
	for _, c := range cats {
		st.ByCategory[c.Category] = c.Count
	}

	// By status.
	type statusCount struct {
		Status string `db:"status"`
		Count  int    `db:"cnt"`
	}
	var statuses []statusCount
	if err := s.db.SelectContext(ctx, &statuses,
		"SELECT status, COUNT(*) as cnt FROM audit_events"+whereClause+" GROUP BY status", args...); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("counting audit events by status: %w", err)
	}
	for _, s := range statuses {
		st.ByStatus[s.Status] = s.Count
	}

	// Events last hour — its own window, but the same exclusions, so a hidden
	// source stays hidden in every number on the page.
	oneHourAgo := time.Now().UTC().Add(-time.Hour)
	hourClause, hourArgs := buildWhereClause(ListOpts{
		Since:          &oneHourAgo,
		ExcludeSources: opts.ExcludeSources,
	})
	var lastHour int
	if err := s.db.GetContext(ctx, &lastHour,
		"SELECT COUNT(*) FROM audit_events"+hourClause, hourArgs...); err != nil {
		return nil, fmt.Errorf("counting last hour audit events: %w", err)
	}
	st.EventsLastHour = lastHour

	return st, nil
}

// checkpointPayload is the detail of a prune checkpoint row.
type checkpointPayload struct {
	PrunedRows        int64  `json:"pruned_rows"`
	PrunedThroughID   int64  `json:"pruned_through_id"`
	PrunedThroughHash string `json:"pruned_through_hash"`
	Cutoff            string `json:"cutoff"`
}

// PruneBefore deletes the oldest events, up to but not including the first
// event (by id) still inside retention, and appends a checkpoint row that
// accounts for them. Deleting an id prefix keeps the chain free of holes, so
// an out-of-order old row stays until the rows before it age out. Returns
// the number of deleted rows; the checkpoint is not counted.
func (s *SQLiteStore) PruneBefore(ctx context.Context, before time.Time) (int, error) {
	s.chainMu.Lock()
	defer s.chainMu.Unlock()

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning audit prune transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	headID, headHash, ok, err := readChainHead(ctx, tx)
	if err != nil || !ok {
		return 0, err
	}
	cutoff := before.Format(time.RFC3339Nano)
	keep, err := firstKeptID(ctx, tx, cutoff, headID)
	if err != nil {
		return 0, err
	}
	var last struct {
		ID   int64  `db:"id"`
		Hash string `db:"hash"`
	}
	err = tx.GetContext(ctx, &last, `SELECT id, hash FROM audit_events WHERE id < ? ORDER BY id DESC LIMIT 1`, keep)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading last pruned audit event: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE id < ?`, keep)
	if err != nil {
		return 0, fmt.Errorf("pruning audit events: %w", err)
	}
	n, _ := res.RowsAffected()

	cp, err := checkpointEvent(n, last.ID, last.Hash, cutoff)
	if err != nil {
		return 0, err
	}
	if err := appendChained(ctx, tx, headHash, []Event{cp}); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing audit prune: %w", err)
	}
	return int(n), nil
}

// firstKeptID is the lowest id still inside retention, or headID+1 when
// every row is older than the cutoff.
func firstKeptID(ctx context.Context, tx *sqlx.Tx, cutoff string, headID int64) (int64, error) {
	var keep int64
	err := tx.GetContext(ctx, &keep, `SELECT id FROM audit_events WHERE timestamp >= ? ORDER BY id LIMIT 1`, cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		return headID + 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("finding first retained audit event: %w", err)
	}
	return keep, nil
}

func checkpointEvent(pruned, throughID int64, throughHash, cutoff string) (Event, error) {
	detail, err := json.Marshal(checkpointPayload{
		PrunedRows: pruned, PrunedThroughID: throughID, PrunedThroughHash: throughHash, Cutoff: cutoff,
	})
	if err != nil {
		return Event{}, fmt.Errorf("encoding prune checkpoint: %w", err)
	}
	return Event{
		Timestamp: time.Now().UTC(),
		Category:  CategoryAudit,
		Action:    ActionPruneCheckpoint,
		Summary:   fmt.Sprintf("Retention pruned %d events up to id %d", pruned, throughID),
		Detail:    string(detail),
		Status:    StatusOK,
		Source:    "retention",
	}, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
