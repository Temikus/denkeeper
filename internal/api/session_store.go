package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// SessionRecord represents a server-tracked session stored in SQLite.
type SessionRecord struct {
	ID         string    `db:"id"           json:"id"`
	Email      string    `db:"email"        json:"email"`
	Scopes     string    `db:"scopes"       json:"-"` // JSON array stored as text
	UserAgent  string    `db:"user_agent"   json:"user_agent"`
	IP         string    `db:"ip"           json:"ip"`
	CreatedAt  time.Time `db:"created_at"   json:"created_at"`
	ExpiresAt  time.Time `db:"expires_at"   json:"expires_at"`
	LastSeenAt time.Time `db:"last_seen_at" json:"last_seen_at"`
}

// SessionStore manages server-tracked sessions in SQLite.
type SessionStore struct {
	db *sqlx.DB
}

const sessionSchema = `
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    scopes TEXT NOT NULL DEFAULT '[]',
    user_agent TEXT DEFAULT '',
    ip TEXT DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sessions_email ON sessions(email);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
`

// NewSessionStore opens or creates a SQLite database for session tracking.
func NewSessionStore(dbPath string) (*SessionStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating session database directory: %w", err)
	}

	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening session database: %w", err)
	}

	if _, err := db.Exec(sessionSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing session schema: %w", err)
	}

	return &SessionStore{db: db}, nil
}

// NewInMemorySessionStore creates an in-memory session store (for testing).
func NewInMemorySessionStore() (*SessionStore, error) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("opening in-memory session database: %w", err)
	}
	if _, err := db.Exec(sessionSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing session schema: %w", err)
	}
	return &SessionStore{db: db}, nil
}

// generateSessionID creates a cryptographically random session ID.
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating session ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Create inserts a new session record and returns its ID.
func (ss *SessionStore) Create(ctx context.Context, email string, scopes []string, userAgent, ip string, expiresAt time.Time) (string, error) {
	id, err := generateSessionID()
	if err != nil {
		return "", err
	}

	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return "", fmt.Errorf("marshaling scopes: %w", err)
	}

	_, err = ss.db.ExecContext(ctx,
		`INSERT INTO sessions (id, email, scopes, user_agent, ip, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, email, string(scopesJSON), userAgent, ip, expiresAt.UTC().Format(time.RFC3339))
	if err != nil {
		return "", fmt.Errorf("inserting session: %w", err)
	}
	return id, nil
}

// Get retrieves a session record by ID. Returns nil if not found or expired.
func (ss *SessionStore) Get(ctx context.Context, id string) (*SessionRecord, error) {
	var rec SessionRecord
	err := ss.db.GetContext(ctx, &rec, `SELECT * FROM sessions WHERE id = ? AND expires_at > ?`, id, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	return &rec, nil
}

// ListByEmail returns all active sessions for a given email.
func (ss *SessionStore) ListByEmail(ctx context.Context, email string) ([]SessionRecord, error) {
	var records []SessionRecord
	err := ss.db.SelectContext(ctx, &records,
		`SELECT * FROM sessions WHERE email = ? AND expires_at > ? ORDER BY last_seen_at DESC`, email, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	return records, nil
}

// Delete removes a session by ID. Returns nil if not found (idempotent).
func (ss *SessionStore) Delete(ctx context.Context, id string) error {
	_, err := ss.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

// DeleteAllByEmail removes all sessions for a given email. Returns count deleted.
func (ss *SessionStore) DeleteAllByEmail(ctx context.Context, email string) (int64, error) {
	result, err := ss.db.ExecContext(ctx, `DELETE FROM sessions WHERE email = ?`, email)
	if err != nil {
		return 0, fmt.Errorf("deleting all sessions: %w", err)
	}
	return result.RowsAffected()
}

// Touch updates the last_seen_at timestamp for a session.
func (ss *SessionStore) Touch(ctx context.Context, id string) error {
	_, err := ss.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("touching session: %w", err)
	}
	return nil
}

// PurgeExpired removes all sessions past their expiry time. Returns count deleted.
func (ss *SessionStore) PurgeExpired(ctx context.Context) (int64, error) {
	result, err := ss.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("purging expired sessions: %w", err)
	}
	return result.RowsAffected()
}

// Count returns the number of active (non-expired) sessions.
func (ss *SessionStore) Count(ctx context.Context) (int, error) {
	var count int
	err := ss.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM sessions WHERE expires_at > ?`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("counting sessions: %w", err)
	}
	return count, nil
}

// Close closes the underlying database connection.
func (ss *SessionStore) Close() error {
	return ss.db.Close()
}

// ScopesFromRecord parses the JSON scopes string into a string slice.
func ScopesFromRecord(rec *SessionRecord) []string {
	var scopes []string
	_ = json.Unmarshal([]byte(rec.Scopes), &scopes)
	return scopes
}
