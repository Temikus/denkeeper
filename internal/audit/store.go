package audit

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
`

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sqlx.DB
}

// Compile-time check.
var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens (or creates) the audit database at dbPath.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating audit database directory: %w", err)
	}

	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening audit database: %w", err)
	}

	if _, err := db.Exec(auditSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing audit schema: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// NewInMemoryStore creates an in-memory audit store (for testing).
func NewInMemoryStore() (*SQLiteStore, error) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(auditSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Insert persists a single audit event.
func (s *SQLiteStore) Insert(ctx context.Context, event Event) error {
	const q = `INSERT INTO audit_events (timestamp, category, action, agent, summary, detail, status, duration_ms, source, conversation_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	ts := event.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx, q,
		ts.Format(time.RFC3339Nano),
		event.Category,
		event.Action,
		event.Agent,
		event.Summary,
		event.Detail,
		event.Status,
		event.DurationMs,
		event.Source,
		event.ConversationID,
	)
	if err != nil {
		return fmt.Errorf("inserting audit event: %w", err)
	}
	return nil
}

// InsertBatch persists multiple audit events in a single transaction.
func (s *SQLiteStore) InsertBatch(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning audit batch transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	const q = `INSERT INTO audit_events (timestamp, category, action, agent, summary, detail, status, duration_ms, source, conversation_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("preparing audit batch statement: %w", err)
	}
	defer stmt.Close() //nolint:errcheck

	for _, event := range events {
		ts := event.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		_, err := stmt.ExecContext(ctx, ts.Format(time.RFC3339Nano),
			event.Category, event.Action, event.Agent, event.Summary,
			event.Detail, event.Status, event.DurationMs, event.Source,
			event.ConversationID,
		)
		if err != nil {
			return fmt.Errorf("inserting audit event in batch: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing audit batch: %w", err)
	}
	return nil
}

// buildWhereClause constructs WHERE conditions from ListOpts.
func buildWhereClause(opts ListOpts) (string, []any) {
	var where []string
	var args []any

	if opts.Category != "" {
		where = append(where, "category = ?")
		args = append(args, opts.Category)
	}
	if opts.Agent != "" {
		where = append(where, "agent = ?")
		args = append(args, opts.Agent)
	}
	if opts.Status != "" {
		where = append(where, "status = ?")
		args = append(args, opts.Status)
	}
	if opts.Source != "" {
		where = append(where, "source = ?")
		args = append(args, opts.Source)
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
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating audit events: %w", err)
	}

	return events, total, nil
}

// Stats returns aggregate counts for the audit dashboard.
func (s *SQLiteStore) Stats(ctx context.Context, since *time.Time) (*Stats, error) {
	st := &Stats{
		ByCategory: make(map[string]int),
		ByStatus:   make(map[string]int),
	}

	sinceStr := ""
	if since != nil {
		sinceStr = since.Format(time.RFC3339Nano)
	}

	// Total + by category.
	var whereClause string
	var args []any
	if sinceStr != "" {
		whereClause = " WHERE timestamp >= ?"
		args = []any{sinceStr}
	}

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

	// Events last hour.
	oneHourAgo := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	var lastHour int
	if err := s.db.GetContext(ctx, &lastHour,
		"SELECT COUNT(*) FROM audit_events WHERE timestamp >= ?", oneHourAgo); err != nil {
		return nil, fmt.Errorf("counting last hour audit events: %w", err)
	}
	st.EventsLastHour = lastHour

	return st, nil
}

// PruneBefore deletes audit events older than the given time.
// Returns the number of deleted rows.
func (s *SQLiteStore) PruneBefore(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM audit_events WHERE timestamp < ?",
		before.Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("pruning audit events: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
