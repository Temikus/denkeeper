package kv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// MaxValueBytes is the default maximum value size in bytes (64 KB).
const MaxValueBytes = 65536

// MaxKeysPerAgent is the default maximum number of keys per agent.
const MaxKeysPerAgent = 1000

// timeLayout is how expires_at is written. Reads and Cleanup compare it to
// datetime('now') as text, so every writer must use exactly this layout.
const timeLayout = "2006-01-02 15:04:05"

const schema = `
CREATE TABLE IF NOT EXISTS kv (
    agent_name TEXT    NOT NULL,
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    PRIMARY KEY (agent_name, key)
);
CREATE INDEX IF NOT EXISTS idx_kv_expires
    ON kv (expires_at) WHERE expires_at IS NOT NULL;
`

// Entry represents a key-value pair with metadata.
type Entry struct {
	Key       string     `db:"key"`
	Value     string     `db:"value"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
	ExpiresAt *time.Time `db:"expires_at"`
}

// Store provides per-agent key-value storage with optional TTL.
type Store interface {
	Get(ctx context.Context, agent, key string) (value string, ok bool, err error)
	// GetEntry is Get with the row metadata (timestamps, expiry).
	GetEntry(ctx context.Context, agent, key string) (Entry, bool, error)
	Set(ctx context.Context, agent, key, value string, ttl time.Duration) error
	Delete(ctx context.Context, agent, key string) error
	List(ctx context.Context, agent, prefix string) ([]Entry, error)
	SetNX(ctx context.Context, agent, key, value string, ttl time.Duration) (bool, error)
	Cleanup(ctx context.Context) error
	Close() error
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db              *sqlx.DB
	maxKeysPerAgent int
	maxValueBytes   int
}

// Option configures a SQLiteStore.
type Option func(*SQLiteStore)

// WithMaxKeysPerAgent sets the maximum number of keys per agent (0 = unlimited).
func WithMaxKeysPerAgent(n int) Option {
	return func(s *SQLiteStore) { s.maxKeysPerAgent = n }
}

// WithMaxValueBytes sets the maximum value size in bytes.
func WithMaxValueBytes(n int) Option {
	return func(s *SQLiteStore) { s.maxValueBytes = n }
}

// NewSQLiteStore opens or creates a SQLite database and applies the KV schema.
func NewSQLiteStore(dbPath string, opts ...Option) (*SQLiteStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing kv schema: %w", err)
	}

	s := &SQLiteStore{
		db:              db,
		maxKeysPerAgent: MaxKeysPerAgent,
		maxValueBytes:   MaxValueBytes,
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// NewInMemoryStore creates an in-memory SQLite store for testing.
func NewInMemoryStore(opts ...Option) (*SQLiteStore, error) {
	db, err := sqlx.Open("sqlite", ":memory:?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening in-memory database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing kv schema: %w", err)
	}

	s := &SQLiteStore{
		db:              db,
		maxKeysPerAgent: MaxKeysPerAgent,
		maxValueBytes:   MaxValueBytes,
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Get returns the value for a key, or ("", false, nil) if not found or expired.
func (s *SQLiteStore) Get(ctx context.Context, agent, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowxContext(ctx,
		`SELECT value FROM kv WHERE agent_name = ? AND key = ? AND (expires_at IS NULL OR expires_at > datetime('now'))`,
		agent, key,
	).Scan(&value)
	if err != nil {
		if isNoRows(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("kv get %q: %w", key, err)
	}
	return value, true, nil
}

// GetEntry returns the full row for a non-expired key.
func (s *SQLiteStore) GetEntry(ctx context.Context, agent, key string) (Entry, bool, error) {
	var e Entry
	err := s.db.GetContext(ctx, &e,
		`SELECT key, value, created_at, updated_at, expires_at FROM kv
		 WHERE agent_name = ? AND key = ? AND (expires_at IS NULL OR expires_at > datetime('now'))`,
		agent, key,
	)
	if err != nil {
		if isNoRows(err) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("kv get %q: %w", key, err)
	}
	return e, true, nil
}

// Set stores a key-value pair. If ttl is zero, the key does not expire.
func (s *SQLiteStore) Set(ctx context.Context, agent, key, value string, ttl time.Duration) error {
	if s.maxValueBytes > 0 && len(value) > s.maxValueBytes {
		return fmt.Errorf("value size %d exceeds maximum %d bytes", len(value), s.maxValueBytes)
	}

	if err := s.checkKeyLimit(ctx, agent, key); err != nil {
		return err
	}

	var expiresAt *string
	if ttl > 0 {
		t := time.Now().UTC().Add(ttl).Format(timeLayout)
		expiresAt = &t
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv (agent_name, key, value, expires_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (agent_name, key) DO UPDATE SET value = excluded.value, updated_at = datetime('now'), expires_at = excluded.expires_at`,
		agent, key, value, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("kv set %q: %w", key, err)
	}
	return nil
}

// Delete removes a key. No error if the key does not exist.
func (s *SQLiteStore) Delete(ctx context.Context, agent, key string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM kv WHERE agent_name = ? AND key = ?`,
		agent, key,
	)
	if err != nil {
		return fmt.Errorf("kv delete %q: %w", key, err)
	}
	return nil
}

// List returns all non-expired keys for an agent, optionally filtered by prefix.
func (s *SQLiteStore) List(ctx context.Context, agent, prefix string) ([]Entry, error) {
	var entries []Entry
	var err error

	if prefix == "" {
		err = s.db.SelectContext(ctx, &entries,
			`SELECT key, value, created_at, updated_at, expires_at FROM kv
			 WHERE agent_name = ? AND (expires_at IS NULL OR expires_at > datetime('now'))
			 ORDER BY key`,
			agent,
		)
	} else {
		err = s.db.SelectContext(ctx, &entries,
			`SELECT key, value, created_at, updated_at, expires_at FROM kv
			 WHERE agent_name = ? AND key LIKE ? AND (expires_at IS NULL OR expires_at > datetime('now'))
			 ORDER BY key`,
			agent, prefix+"%",
		)
	}
	if err != nil {
		return nil, fmt.Errorf("kv list: %w", err)
	}
	if entries == nil {
		entries = []Entry{}
	}
	return entries, nil
}

// SetNX stores a key only if it doesn't already exist (atomic).
// Returns true if the key was set, false if it already existed (and is not expired).
func (s *SQLiteStore) SetNX(ctx context.Context, agent, key, value string, ttl time.Duration) (bool, error) {
	if s.maxValueBytes > 0 && len(value) > s.maxValueBytes {
		return false, fmt.Errorf("value size %d exceeds maximum %d bytes", len(value), s.maxValueBytes)
	}

	if err := s.checkKeyLimit(ctx, agent, key); err != nil {
		return false, err
	}

	// Clean up expired key first so SetNX can reuse the slot.
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM kv WHERE agent_name = ? AND key = ? AND expires_at IS NOT NULL AND expires_at <= datetime('now')`,
		agent, key,
	)

	var expiresAt *string
	if ttl > 0 {
		t := time.Now().UTC().Add(ttl).Format(timeLayout)
		expiresAt = &t
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO kv (agent_name, key, value, expires_at) VALUES (?, ?, ?, ?)`,
		agent, key, value, expiresAt,
	)
	if err != nil {
		return false, fmt.Errorf("kv set_nx %q: %w", key, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("kv set_nx rows affected: %w", err)
	}
	return n > 0, nil
}

// Cleanup removes all expired keys.
func (s *SQLiteStore) Cleanup(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at <= datetime('now')`,
	)
	if err != nil {
		return fmt.Errorf("kv cleanup: %w", err)
	}
	return nil
}

// BackfillCount is one agent-and-namespace row of a TTL backfill.
type BackfillCount struct {
	Agent     string
	Namespace string // the matched default_ttl prefix
	TTL       time.Duration
	Keys      int
	// AlreadyExpired counts keys whose new expiry is already past: reads hide
	// them at once and the next Cleanup deletes them.
	AlreadyExpired int
}

type backfillRow struct {
	agent, key, namespace string
	ttl                   time.Duration
	expiresAt             time.Time
}

// BackfillTTL gives every key with no expiry under a defaulted prefix
// expires_at = updated_at + ttl. The store does not record the writer, so keys
// stored without an expiry on purpose (kv_setnx locks, REST, ttl "0s") are
// selected too. Keys with any expiry are never touched, and value, created_at
// and updated_at never change. With dryRun it only counts.
// Rows are sorted by agent, then namespace. Not on Store: only the CLI uses it.
func (s *SQLiteStore) BackfillTTL(ctx context.Context, defaults map[string]time.Duration, dryRun bool) ([]BackfillCount, error) {
	if len(defaults) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("kv backfill: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := selectBackfill(ctx, tx, defaults)
	if err != nil {
		return nil, err
	}
	if !dryRun {
		if rows, err = applyBackfill(ctx, tx, rows); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("kv backfill: commit: %w", err)
		}
	}
	return summarizeBackfill(rows, time.Now().UTC()), nil
}

// selectBackfill matches in Go, not SQL: LIKE ignores ASCII case and treats
// _ and % as wildcards, so it would select keys kv_set would not.
func selectBackfill(ctx context.Context, tx *sqlx.Tx, defaults map[string]time.Duration) ([]backfillRow, error) {
	var candidates []struct {
		Agent     string    `db:"agent_name"`
		Key       string    `db:"key"`
		UpdatedAt time.Time `db:"updated_at"`
	}
	if err := tx.SelectContext(ctx, &candidates,
		`SELECT agent_name, key, updated_at FROM kv WHERE expires_at IS NULL`,
	); err != nil {
		return nil, fmt.Errorf("kv backfill: select: %w", err)
	}
	var rows []backfillRow
	for _, c := range candidates {
		prefix, ttl := DefaultTTLFor(defaults, c.Key)
		if ttl <= 0 {
			continue
		}
		rows = append(rows, backfillRow{agent: c.Agent, key: c.Key, namespace: prefix,
			ttl: ttl, expiresAt: c.UpdatedAt.UTC().Add(ttl)})
	}
	return rows, nil
}

// applyBackfill returns the rows it actually updated. The repeated
// expires_at IS NULL keeps a TTL the running server set meanwhile.
func applyBackfill(ctx context.Context, tx *sqlx.Tx, rows []backfillRow) ([]backfillRow, error) {
	updated := rows[:0]
	for _, r := range rows {
		res, err := tx.ExecContext(ctx,
			`UPDATE kv SET expires_at = ? WHERE agent_name = ? AND key = ? AND expires_at IS NULL`,
			r.expiresAt.Format(timeLayout), r.agent, r.key)
		if err != nil {
			return nil, fmt.Errorf("kv backfill %q: %w", r.key, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, fmt.Errorf("kv backfill %q rows affected: %w", r.key, err)
		} else if n > 0 {
			updated = append(updated, r)
		}
	}
	return updated, nil
}

func summarizeBackfill(rows []backfillRow, now time.Time) []BackfillCount {
	index := map[[2]string]int{}
	var out []BackfillCount
	for _, r := range rows {
		g := [2]string{r.agent, r.namespace}
		i, ok := index[g]
		if !ok {
			i = len(out)
			index[g] = i
			out = append(out, BackfillCount{Agent: r.agent, Namespace: r.namespace, TTL: r.ttl})
		}
		out[i].Keys++
		if !r.expiresAt.After(now) {
			out[i].AlreadyExpired++
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Agent != out[j].Agent {
			return out[i].Agent < out[j].Agent
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out
}

// Close releases the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// checkKeyLimit ensures the agent has not exceeded the per-agent key limit.
// It skips the check if the key already exists (updates are always allowed).
func (s *SQLiteStore) checkKeyLimit(ctx context.Context, agent, key string) error {
	if s.maxKeysPerAgent <= 0 {
		return nil
	}

	// Check if key already exists (update path — no limit check needed).
	var exists int
	if err := s.db.QueryRowxContext(ctx,
		`SELECT 1 FROM kv WHERE agent_name = ? AND key = ?`,
		agent, key,
	).Scan(&exists); err == nil {
		return nil // key exists, this is an update
	}

	var count int
	if err := s.db.QueryRowxContext(ctx,
		`SELECT COUNT(*) FROM kv WHERE agent_name = ?`,
		agent,
	).Scan(&count); err != nil {
		return fmt.Errorf("kv key count: %w", err)
	}

	if count >= s.maxKeysPerAgent {
		return fmt.Errorf("agent %q has reached the maximum of %d keys", agent, s.maxKeysPerAgent)
	}
	return nil
}

func isNoRows(err error) bool {
	return err.Error() == "sql: no rows in result set"
}
