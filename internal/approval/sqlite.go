package approval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS approvals (
    id TEXT PRIMARY KEY,
    agent_name TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    summary TEXT NOT NULL,
    payload TEXT NOT NULL,
    callback_data TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL,
    adapter_name TEXT NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    resolved_at DATETIME,
    resolved_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_approvals_status ON approvals(status, created_at);
CREATE INDEX IF NOT EXISTS idx_approvals_callback ON approvals(callback_data);
CREATE INDEX IF NOT EXISTS idx_approvals_expires ON approvals(expires_at) WHERE status = 'pending';
`

const autoApproveSchema = `
CREATE TABLE IF NOT EXISTS auto_approve_rules (
    id TEXT PRIMARY KEY,
    agent_name TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'permanent',
    conversation_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_auto_approve_agent_tool ON auto_approve_rules(agent_name, tool_name);
`

// migrationAddExpiresAt is applied after schema creation to add the expires_at
// column to pre-existing databases that were created before this column existed.
const migrationAddExpiresAt = `ALTER TABLE approvals ADD COLUMN expires_at DATETIME`

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sqlx.DB
}

// NewSQLiteStore opens or creates a SQLite database at the given path and
// applies the approval schema. The file is opened with WAL mode so it can
// coexist with the memory store's connection to the same file.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
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
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	// Idempotent migration: add expires_at to pre-existing databases.
	if _, err := db.Exec(migrationAddExpiresAt); err != nil && !isDuplicateColumn(err) {
		_ = db.Close()
		return nil, fmt.Errorf("migrating schema (expires_at): %w", err)
	}

	if _, err := db.Exec(autoApproveSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing auto-approve schema: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// NewInMemoryStore creates an in-memory SQLite approval store (for testing).
func NewInMemoryStore() (*SQLiteStore, error) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	// In-memory DBs are always fresh; migration is a no-op but run for consistency.
	if _, err := db.Exec(migrationAddExpiresAt); err != nil && !isDuplicateColumn(err) {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(autoApproveSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Create(ctx context.Context, req Request) (string, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO approvals
		 (id, agent_name, kind, status, summary, payload, callback_data, external_id, adapter_name, conversation_id, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.AgentName, string(req.Kind), string(StatusPending),
		req.Summary, req.Payload, req.CallbackData,
		req.ExternalID, req.AdapterName, req.ConversationID,
		req.ExpiresAt,
	)
	if err != nil {
		return "", fmt.Errorf("creating approval: %w", err)
	}
	return req.ID, nil
}

const selectCols = `id, agent_name, kind, status, summary, payload, callback_data,
		        external_id, adapter_name, conversation_id, created_at, expires_at, resolved_at, resolved_by`

func (s *SQLiteStore) Get(ctx context.Context, id string) (*Request, error) {
	var req Request
	err := s.db.GetContext(ctx, &req,
		`SELECT `+selectCols+` FROM approvals WHERE id = ?`, id,
	)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting approval: %w", err)
	}
	return &req, nil
}

func (s *SQLiteStore) GetByCallbackData(ctx context.Context, callbackData string) (*Request, error) {
	var req Request
	err := s.db.GetContext(ctx, &req,
		`SELECT `+selectCols+` FROM approvals WHERE callback_data = ? ORDER BY created_at DESC LIMIT 1`,
		callbackData,
	)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting approval by callback: %w", err)
	}
	return &req, nil
}

func (s *SQLiteStore) List(ctx context.Context, status Status) ([]Request, error) {
	var rows []Request
	var err error
	if status == "" {
		err = s.db.SelectContext(ctx, &rows,
			`SELECT `+selectCols+` FROM approvals ORDER BY created_at DESC`,
		)
	} else {
		err = s.db.SelectContext(ctx, &rows,
			`SELECT `+selectCols+` FROM approvals WHERE status = ? ORDER BY created_at DESC`, string(status),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("listing approvals: %w", err)
	}
	return rows, nil
}

func (s *SQLiteStore) Resolve(ctx context.Context, id string, status Status, resolvedBy string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE approvals SET status = ?, resolved_at = ?, resolved_by = ?
		 WHERE id = ? AND status = 'pending'`,
		string(status), now, resolvedBy, id,
	)
	if err != nil {
		return fmt.Errorf("resolving approval: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resolving approval (rows): %w", err)
	}
	if n == 0 {
		// Either the ID doesn't exist or it's already resolved.
		if _, getErr := s.Get(ctx, id); getErr != nil {
			return ErrNotFound
		}
		return ErrAlreadyResolved
	}
	return nil
}

func (s *SQLiteStore) ResolveByCallbackPrefix(ctx context.Context, prefix string, status Status, resolvedBy string) (*Request, error) {
	// Look up the pending approval whose callback_data matches the prefix.
	var req Request
	err := s.db.GetContext(ctx, &req,
		`SELECT `+selectCols+` FROM approvals WHERE callback_data = ? AND status = 'pending'`, prefix,
	)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("looking up approval by callback: %w", err)
	}

	if err := s.Resolve(ctx, req.ID, status, resolvedBy); err != nil {
		return nil, err
	}

	// Return the updated record.
	return s.Get(ctx, req.ID)
}

func (s *SQLiteStore) ExpirePending(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE approvals SET status = 'expired', resolved_at = ?, resolved_by = 'expired'
		 WHERE status = 'pending'`, now,
	)
	if err != nil {
		return 0, fmt.Errorf("expiring pending approvals: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("expiring pending approvals (rows): %w", err)
	}
	return int(n), nil
}

func (s *SQLiteStore) ExpireBefore(ctx context.Context, deadline time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE approvals SET status = 'expired', resolved_at = ?, resolved_by = 'expired'
		 WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at <= ?`,
		deadline.UTC(), deadline.UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("expiring old approvals: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("expiring old approvals (rows): %w", err)
	}
	return int(n), nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// --- AutoApproveStore implementation ---

const autoApproveSelectCols = `id, agent_name, tool_name, scope, conversation_id, created_at, created_by`

func (s *SQLiteStore) CreateAutoApproveRule(ctx context.Context, rule AutoApproveRule) (string, error) {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auto_approve_rules (id, agent_name, tool_name, scope, conversation_id, created_by)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.AgentName, rule.ToolName, string(rule.Scope), rule.ConversationID, rule.CreatedBy,
	)
	if err != nil {
		return "", fmt.Errorf("creating auto-approve rule: %w", err)
	}
	return rule.ID, nil
}

func (s *SQLiteStore) DeleteAutoApproveRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM auto_approve_rules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting auto-approve rule: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("deleting auto-approve rule (rows): %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) ListAutoApproveRules(ctx context.Context, agentName string) ([]AutoApproveRule, error) {
	var rows []AutoApproveRule
	var err error
	if agentName == "" {
		err = s.db.SelectContext(ctx, &rows,
			`SELECT `+autoApproveSelectCols+` FROM auto_approve_rules ORDER BY created_at DESC`)
	} else {
		err = s.db.SelectContext(ctx, &rows,
			`SELECT `+autoApproveSelectCols+` FROM auto_approve_rules WHERE agent_name = ? ORDER BY created_at DESC`,
			agentName)
	}
	if err != nil {
		return nil, fmt.Errorf("listing auto-approve rules: %w", err)
	}
	return rows, nil
}

func (s *SQLiteStore) MatchAutoApproveRule(ctx context.Context, agentName, toolName string) (*AutoApproveRule, error) {
	var rule AutoApproveRule
	err := s.db.GetContext(ctx, &rule,
		`SELECT `+autoApproveSelectCols+` FROM auto_approve_rules
		 WHERE agent_name = ? AND tool_name = ? AND scope = 'permanent' LIMIT 1`,
		agentName, toolName,
	)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("matching auto-approve rule: %w", err)
	}
	return &rule, nil
}

// isNotFound returns true for the error SQLite returns on no-row queries.
func isNotFound(err error) bool {
	return err != nil && err.Error() == "sql: no rows in result set"
}

// isDuplicateColumn returns true when SQLite rejects an ALTER TABLE ADD COLUMN
// because the column already exists (idempotent migration guard).
func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}
