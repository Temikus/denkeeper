package agent

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// StoredMessage represents a message persisted in the memory store.
type StoredMessage struct {
	ID               int64     `db:"id"`
	ConversationID   string    `db:"conversation_id"`
	Role             string    `db:"role"`
	Content          string    `db:"content"`
	TokensUsed       int       `db:"tokens_used"`
	Cost             float64   `db:"cost"`
	Model            string    `db:"model"`
	Provider         string    `db:"provider"`
	TokensPrompt     int       `db:"tokens_prompt"`
	TokensCompletion int       `db:"tokens_completion"`
	TokensCached     int       `db:"tokens_cached"`
	CreatedAt        time.Time `db:"created_at"`
}

// ConversationInfo provides metadata about a conversation.
type ConversationInfo struct {
	ID           string    `db:"id"            json:"id"`
	Adapter      string    `db:"adapter"       json:"adapter"`
	ExternalID   string    `db:"external_id"   json:"external_id"`
	CreatedAt    time.Time `db:"created_at"    json:"created_at"`
	MessageCount int       `db:"message_count" json:"message_count"`
}

// ToolCallRecord represents a persisted tool call linked to an assistant message.
type ToolCallRecord struct {
	ID             int64     `db:"id"              json:"id"`
	MessageID      int64     `db:"message_id"      json:"message_id"`
	ConversationID string    `db:"conversation_id" json:"conversation_id"`
	ToolName       string    `db:"tool_name"       json:"tool_name"`
	ServerName     string    `db:"server_name"     json:"server_name"`
	Round          int       `db:"round"           json:"round"`
	DurationMs     int64     `db:"duration_ms"     json:"duration_ms"`
	Success        bool      `db:"success"         json:"success"`
	ErrorMsg       string    `db:"error_msg"       json:"error_msg,omitempty"`
	CreatedAt      time.Time `db:"created_at"      json:"created_at"`
}

// SkillUsageRecord represents a skill matched for a user message.
type SkillUsageRecord struct {
	ID             int64     `db:"id"              json:"id"`
	MessageID      int64     `db:"message_id"      json:"message_id"`
	ConversationID string    `db:"conversation_id" json:"conversation_id"`
	SkillName      string    `db:"skill_name"      json:"skill_name"`
	MatchType      string    `db:"match_type"      json:"match_type"`
	CreatedAt      time.Time `db:"created_at"      json:"created_at"`
}

// ConversationStatsRow holds aggregated telemetry for a conversation.
type ConversationStatsRow struct {
	ConversationID  string    `db:"conversation_id"   json:"conversation_id"`
	TotalMessages   int       `db:"total_messages"    json:"total_messages"`
	TotalCost       float64   `db:"total_cost"        json:"total_cost"`
	TotalPrompt     int       `db:"total_tokens_prompt"      json:"total_tokens_prompt"`
	TotalCompletion int       `db:"total_tokens_completion"  json:"total_tokens_completion"`
	TotalCached     int       `db:"total_tokens_cached"      json:"total_tokens_cached"`
	TotalToolCalls  int       `db:"total_tool_calls"  json:"total_tool_calls"`
	TotalToolErrors int       `db:"total_tool_errors" json:"total_tool_errors"`
	LastModel       string    `db:"last_model"        json:"last_model"`
	LastProvider    string    `db:"last_provider"     json:"last_provider"`
	UpdatedAt       time.Time `db:"updated_at"        json:"updated_at"`
}

// ConversationInfoWithStats combines conversation metadata with telemetry stats.
type ConversationInfoWithStats struct {
	ConversationInfo
	TotalCost    float64 `db:"total_cost"    json:"total_cost"`
	TotalPrompt  int     `db:"total_tokens_prompt"  json:"total_tokens_prompt"`
	TotalCompl   int     `db:"total_tokens_completion" json:"total_tokens_completion"`
	LastModel    string  `db:"last_model"    json:"last_model"`
	LastProvider string  `db:"last_provider" json:"last_provider"`
}

// MemoryStore defines the interface for conversation persistence.
type MemoryStore interface {
	GetOrCreateConversation(ctx context.Context, adapter, externalID string) (string, error)
	// GetOrCreateConversationByID ensures a conversation row exists for the
	// given convID. The adapter and externalID are stored as metadata so that
	// ListConversations can report the correct source (e.g. "ws", "api",
	// "sched"). If the row already exists (INSERT OR IGNORE) the stored
	// adapter/externalID are left unchanged.
	GetOrCreateConversationByID(ctx context.Context, convID, adapter, externalID string) error
	AddMessage(ctx context.Context, convID string, msg StoredMessage) (int64, error)
	GetMessages(ctx context.Context, convID string, limit int) ([]StoredMessage, error)
	ListConversations(ctx context.Context) ([]ConversationInfo, error)
	// DeleteConversation removes a conversation and all its messages by ID.
	// Returns nil if the conversation does not exist (idempotent).
	DeleteConversation(ctx context.Context, convID string) error
	Close() error
}

// TelemetryStore extends MemoryStore with telemetry persistence methods.
// Implementations can be obtained by type-asserting a MemoryStore.
type TelemetryStore interface {
	AddToolCalls(ctx context.Context, convID string, messageID int64, calls []ToolCallRecord) error
	AddSkillUsages(ctx context.Context, convID string, messageID int64, skills []SkillUsageRecord) error
	UpdateConversationStats(ctx context.Context, convID string, msg StoredMessage, toolCallCount, toolErrorCount int) error
	GetConversationStats(ctx context.Context, convID string) (*ConversationStatsRow, error)
	ListConversationsWithStats(ctx context.Context) ([]ConversationInfoWithStats, error)
	GetToolCalls(ctx context.Context, convID string) ([]ToolCallRecord, error)
	GetSkillUsages(ctx context.Context, convID string) ([]SkillUsageRecord, error)
	GetTelemetrySummary(ctx context.Context, since, until *time.Time) (*TelemetrySummary, error)
	PruneByCount(ctx context.Context, maxConversations int) (int, error)
}

// SQLiteMemoryStore implements MemoryStore and TelemetryStore using SQLite.
type SQLiteMemoryStore struct {
	db *sqlx.DB
}

// Compile-time interface checks.
var (
	_ MemoryStore        = (*SQLiteMemoryStore)(nil)
	_ TelemetryStore     = (*SQLiteMemoryStore)(nil)
	_ ActiveChannelStore = (*SQLiteMemoryStore)(nil)
)

const schema = `
CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    adapter TEXT NOT NULL,
    external_id TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_conversations_adapter_ext ON conversations(adapter, external_id);

CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    tokens_used INTEGER DEFAULT 0,
    cost REAL DEFAULT 0.0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id, created_at);
`

// telemetryMigrations adds columns to the messages table for telemetry data.
// Each is idempotent — rerunning against an already-migrated DB is safe.
var telemetryMigrations = []string{
	`ALTER TABLE messages ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN provider TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE messages ADD COLUMN tokens_prompt INTEGER DEFAULT 0`,
	`ALTER TABLE messages ADD COLUMN tokens_completion INTEGER DEFAULT 0`,
	`ALTER TABLE messages ADD COLUMN tokens_cached INTEGER DEFAULT 0`,
}

const telemetryTablesSchema = `
CREATE TABLE IF NOT EXISTS tool_calls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL REFERENCES messages(id),
    conversation_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    server_name TEXT NOT NULL DEFAULT '',
    round INTEGER NOT NULL DEFAULT 1,
    duration_ms INTEGER DEFAULT 0,
    success INTEGER NOT NULL DEFAULT 1,
    error_msg TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_msg ON tool_calls(message_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_conv ON tool_calls(conversation_id, created_at);

CREATE TABLE IF NOT EXISTS message_skills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL REFERENCES messages(id),
    conversation_id TEXT NOT NULL,
    skill_name TEXT NOT NULL,
    match_type TEXT NOT NULL DEFAULT 'always',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_msg_skills_msg ON message_skills(message_id);
CREATE INDEX IF NOT EXISTS idx_msg_skills_conv ON message_skills(conversation_id);

CREATE TABLE IF NOT EXISTS conversation_stats (
    conversation_id TEXT PRIMARY KEY REFERENCES conversations(id),
    total_messages INTEGER DEFAULT 0,
    total_cost REAL DEFAULT 0.0,
    total_tokens_prompt INTEGER DEFAULT 0,
    total_tokens_completion INTEGER DEFAULT 0,
    total_tokens_cached INTEGER DEFAULT 0,
    total_tool_calls INTEGER DEFAULT 0,
    total_tool_errors INTEGER DEFAULT 0,
    last_model TEXT NOT NULL DEFAULT '',
    last_provider TEXT NOT NULL DEFAULT '',
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

const channelSchema = `
CREATE TABLE IF NOT EXISTS active_channels (
    adapter_key TEXT PRIMARY KEY,
    channel_name TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

// initDB runs the base schema then applies telemetry migrations.
func initDB(db *sqlx.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("initializing schema: %w", err)
	}
	for _, m := range telemetryMigrations {
		if _, err := db.Exec(m); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("migrating schema: %w", err)
		}
	}
	if _, err := db.Exec(telemetryTablesSchema); err != nil {
		return fmt.Errorf("initializing telemetry schema: %w", err)
	}
	if _, err := db.Exec(channelSchema); err != nil {
		return fmt.Errorf("initializing channel schema: %w", err)
	}
	return nil
}

// isDuplicateColumn returns true when SQLite rejects an ALTER TABLE ADD COLUMN
// because the column already exists (idempotent migration guard).
func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}

// NewSQLiteMemoryStore opens or creates a SQLite database at the given path.
func NewSQLiteMemoryStore(dbPath string) (*SQLiteMemoryStore, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	db, err := sqlx.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := initDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteMemoryStore{db: db}, nil
}

// NewInMemoryStore creates an in-memory SQLite store (for testing).
func NewInMemoryStore() (*SQLiteMemoryStore, error) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	if err := initDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteMemoryStore{db: db}, nil
}

func (s *SQLiteMemoryStore) GetOrCreateConversation(ctx context.Context, adapterName, externalID string) (string, error) {
	convID := adapterName + ":" + externalID

	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO conversations (id, adapter, external_id) VALUES (?, ?, ?)`,
		convID, adapterName, externalID,
	)
	if err != nil {
		return "", fmt.Errorf("creating conversation: %w", err)
	}

	return convID, nil
}

func (s *SQLiteMemoryStore) GetOrCreateConversationByID(ctx context.Context, convID, adapter, externalID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO conversations (id, adapter, external_id) VALUES (?, ?, ?)`,
		convID, adapter, externalID,
	)
	if err != nil {
		return fmt.Errorf("creating conversation: %w", err)
	}
	return nil
}

func (s *SQLiteMemoryStore) AddMessage(ctx context.Context, convID string, msg StoredMessage) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, tokens_used, cost, model, provider, tokens_prompt, tokens_completion, tokens_cached)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		convID, msg.Role, msg.Content, msg.TokensUsed, msg.Cost,
		msg.Model, msg.Provider, msg.TokensPrompt, msg.TokensCompletion, msg.TokensCached,
	)
	if err != nil {
		return 0, fmt.Errorf("adding message: %w", err)
	}
	id, _ := result.LastInsertId()
	return id, nil
}

func (s *SQLiteMemoryStore) GetMessages(ctx context.Context, convID string, limit int) ([]StoredMessage, error) {
	var messages []StoredMessage
	err := s.db.SelectContext(ctx, &messages,
		`SELECT id, conversation_id, role, content, tokens_used, cost,
		        model, provider, tokens_prompt, tokens_completion, tokens_cached, created_at
		 FROM messages WHERE conversation_id = ?
		 ORDER BY created_at DESC LIMIT ?`,
		convID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting messages: %w", err)
	}
	slices.Reverse(messages)
	return messages, nil
}

func (s *SQLiteMemoryStore) ListConversations(ctx context.Context) ([]ConversationInfo, error) {
	var convos []ConversationInfo
	err := s.db.SelectContext(ctx, &convos,
		`SELECT c.id, c.adapter, c.external_id, c.created_at,
		        COALESCE(m.cnt, 0) AS message_count
		 FROM conversations c
		 LEFT JOIN (SELECT conversation_id, COUNT(*) AS cnt FROM messages GROUP BY conversation_id) m
		   ON m.conversation_id = c.id
		 ORDER BY c.created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing conversations: %w", err)
	}
	return convos, nil
}

func (s *SQLiteMemoryStore) DeleteConversation(ctx context.Context, convID string) error {
	// Delete telemetry tables first (FK to messages), then messages, then conversation.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tool_calls WHERE conversation_id = ?`, convID); err != nil {
		return fmt.Errorf("deleting tool calls: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM message_skills WHERE conversation_id = ?`, convID); err != nil {
		return fmt.Errorf("deleting skill usages: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM conversation_stats WHERE conversation_id = ?`, convID); err != nil {
		return fmt.Errorf("deleting conversation stats: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM messages WHERE conversation_id = ?`, convID); err != nil {
		return fmt.Errorf("deleting messages: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, convID); err != nil {
		return fmt.Errorf("deleting conversation: %w", err)
	}
	return nil
}

// ConversationCost returns the total cost of all messages in a conversation.
func (s *SQLiteMemoryStore) ConversationCost(ctx context.Context, convID string) (float64, error) {
	var cost float64
	err := s.db.GetContext(ctx, &cost,
		`SELECT COALESCE(SUM(cost), 0) FROM messages WHERE conversation_id = ?`, convID)
	if err != nil {
		return 0, fmt.Errorf("computing conversation cost: %w", err)
	}
	return cost, nil
}

// CountConversationsBefore returns the number of conversations created before the given time.
func (s *SQLiteMemoryStore) CountConversationsBefore(ctx context.Context, before time.Time) (int, error) {
	var count int
	err := s.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM conversations WHERE created_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("counting old conversations: %w", err)
	}
	return count, nil
}

// PruneConversations deletes all conversations (and their messages, tool calls,
// skill usages, and stats) created before the given time.
// Returns the number of conversations deleted.
func (s *SQLiteMemoryStore) PruneConversations(ctx context.Context, before time.Time) (int, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("starting prune transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	oldConvFilter := `SELECT id FROM conversations WHERE created_at < ?`
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tool_calls WHERE conversation_id IN (`+oldConvFilter+`)`, before); err != nil {
		return 0, fmt.Errorf("deleting old tool calls: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM message_skills WHERE conversation_id IN (`+oldConvFilter+`)`, before); err != nil {
		return 0, fmt.Errorf("deleting old skill usages: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM conversation_stats WHERE conversation_id IN (`+oldConvFilter+`)`, before); err != nil {
		return 0, fmt.Errorf("deleting old conversation stats: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM messages WHERE conversation_id IN (`+oldConvFilter+`)`, before); err != nil {
		return 0, fmt.Errorf("deleting old messages: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`DELETE FROM conversations WHERE created_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("deleting old conversations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing prune transaction: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// AddToolCalls persists tool call records linked to an assistant message.
func (s *SQLiteMemoryStore) AddToolCalls(ctx context.Context, convID string, messageID int64, calls []ToolCallRecord) error {
	if len(calls) == 0 {
		return nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting tool calls transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO tool_calls (message_id, conversation_id, tool_name, server_name, round, duration_ms, success, error_msg)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing tool call insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, tc := range calls {
		successInt := 1
		if !tc.Success {
			successInt = 0
		}
		if _, err := stmt.ExecContext(ctx, messageID, convID, tc.ToolName, tc.ServerName, tc.Round, tc.DurationMs, successInt, tc.ErrorMsg); err != nil {
			return fmt.Errorf("inserting tool call %q: %w", tc.ToolName, err)
		}
	}
	return tx.Commit()
}

// AddSkillUsages persists skill usage records linked to a user message.
func (s *SQLiteMemoryStore) AddSkillUsages(ctx context.Context, convID string, messageID int64, skills []SkillUsageRecord) error {
	if len(skills) == 0 {
		return nil
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting skill usages transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO message_skills (message_id, conversation_id, skill_name, match_type)
		 VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing skill usage insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, su := range skills {
		if _, err := stmt.ExecContext(ctx, messageID, convID, su.SkillName, su.MatchType); err != nil {
			return fmt.Errorf("inserting skill usage %q: %w", su.SkillName, err)
		}
	}
	return tx.Commit()
}

// UpdateConversationStats incrementally updates the conversation_stats row.
func (s *SQLiteMemoryStore) UpdateConversationStats(ctx context.Context, convID string, msg StoredMessage, toolCallCount, toolErrorCount int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO conversation_stats (conversation_id, total_messages, total_cost, total_tokens_prompt, total_tokens_completion, total_tokens_cached, total_tool_calls, total_tool_errors, last_model, last_provider, updated_at)
		 VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(conversation_id) DO UPDATE SET
		   total_messages = total_messages + 1,
		   total_cost = total_cost + excluded.total_cost,
		   total_tokens_prompt = total_tokens_prompt + excluded.total_tokens_prompt,
		   total_tokens_completion = total_tokens_completion + excluded.total_tokens_completion,
		   total_tokens_cached = total_tokens_cached + excluded.total_tokens_cached,
		   total_tool_calls = total_tool_calls + excluded.total_tool_calls,
		   total_tool_errors = total_tool_errors + excluded.total_tool_errors,
		   last_model = excluded.last_model,
		   last_provider = excluded.last_provider,
		   updated_at = CURRENT_TIMESTAMP`,
		convID, msg.Cost, msg.TokensPrompt, msg.TokensCompletion, msg.TokensCached,
		toolCallCount, toolErrorCount, msg.Model, msg.Provider,
	)
	if err != nil {
		return fmt.Errorf("updating conversation stats: %w", err)
	}
	return nil
}

// GetConversationStats returns the aggregated telemetry for a conversation.
func (s *SQLiteMemoryStore) GetConversationStats(ctx context.Context, convID string) (*ConversationStatsRow, error) {
	var row ConversationStatsRow
	err := s.db.GetContext(ctx, &row,
		`SELECT conversation_id, total_messages, total_cost, total_tokens_prompt,
		        total_tokens_completion, total_tokens_cached, total_tool_calls,
		        total_tool_errors, last_model, last_provider, updated_at
		 FROM conversation_stats WHERE conversation_id = ?`, convID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("getting conversation stats: %w", err)
	}
	return &row, nil
}

// ListConversationsWithStats returns conversations joined with their telemetry stats.
func (s *SQLiteMemoryStore) ListConversationsWithStats(ctx context.Context) ([]ConversationInfoWithStats, error) {
	var results []ConversationInfoWithStats
	err := s.db.SelectContext(ctx, &results,
		`SELECT c.id, c.adapter, c.external_id, c.created_at,
		        COALESCE(m.cnt, 0) AS message_count,
		        COALESCE(cs.total_cost, 0) AS total_cost,
		        COALESCE(cs.total_tokens_prompt, 0) AS total_tokens_prompt,
		        COALESCE(cs.total_tokens_completion, 0) AS total_tokens_completion,
		        COALESCE(cs.last_model, '') AS last_model,
		        COALESCE(cs.last_provider, '') AS last_provider
		 FROM conversations c
		 LEFT JOIN (SELECT conversation_id, COUNT(*) AS cnt FROM messages GROUP BY conversation_id) m
		   ON m.conversation_id = c.id
		 LEFT JOIN conversation_stats cs ON cs.conversation_id = c.id
		 ORDER BY c.created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing conversations with stats: %w", err)
	}
	return results, nil
}

// toolCallRow is the raw DB scan target for tool_calls rows.
// The success column is INTEGER (0/1) which some SQLite drivers may not
// auto-scan to bool, so we use int and convert explicitly.
type toolCallRow struct {
	ID             int64     `db:"id"`
	MessageID      int64     `db:"message_id"`
	ConversationID string    `db:"conversation_id"`
	ToolName       string    `db:"tool_name"`
	ServerName     string    `db:"server_name"`
	Round          int       `db:"round"`
	DurationMs     int64     `db:"duration_ms"`
	Success        int       `db:"success"`
	ErrorMsg       string    `db:"error_msg"`
	CreatedAt      time.Time `db:"created_at"`
}

// GetToolCalls returns all tool call records for a conversation.
func (s *SQLiteMemoryStore) GetToolCalls(ctx context.Context, convID string) ([]ToolCallRecord, error) {
	var rows []toolCallRow
	err := s.db.SelectContext(ctx, &rows,
		`SELECT id, message_id, conversation_id, tool_name, server_name, round, duration_ms, success, error_msg, created_at
		 FROM tool_calls WHERE conversation_id = ?
		 ORDER BY created_at ASC`, convID)
	if err != nil {
		return nil, fmt.Errorf("getting tool calls: %w", err)
	}
	records := make([]ToolCallRecord, len(rows))
	for i, r := range rows {
		records[i] = ToolCallRecord{
			ID: r.ID, MessageID: r.MessageID, ConversationID: r.ConversationID,
			ToolName: r.ToolName, ServerName: r.ServerName, Round: r.Round,
			DurationMs: r.DurationMs, Success: r.Success != 0, ErrorMsg: r.ErrorMsg,
			CreatedAt: r.CreatedAt,
		}
	}
	return records, nil
}

// GetSkillUsages returns all skill usage records for a conversation.
func (s *SQLiteMemoryStore) GetSkillUsages(ctx context.Context, convID string) ([]SkillUsageRecord, error) {
	var records []SkillUsageRecord
	err := s.db.SelectContext(ctx, &records,
		`SELECT id, message_id, conversation_id, skill_name, match_type, created_at
		 FROM message_skills WHERE conversation_id = ?
		 ORDER BY created_at ASC`, convID)
	if err != nil {
		return nil, fmt.Errorf("getting skill usages: %w", err)
	}
	return records, nil
}

// PruneByCount deletes the oldest conversations when the total exceeds maxConversations.
// Returns the number of conversations deleted.
func (s *SQLiteMemoryStore) PruneByCount(ctx context.Context, maxConversations int) (int, error) {
	if maxConversations <= 0 {
		return 0, nil
	}

	var total int
	if err := s.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM conversations`); err != nil {
		return 0, fmt.Errorf("counting conversations: %w", err)
	}
	excess := total - maxConversations
	if excess <= 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("starting prune-by-count transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	oldestFilter := `SELECT id FROM conversations ORDER BY created_at ASC LIMIT ?`
	if _, err := tx.ExecContext(ctx, `DELETE FROM tool_calls WHERE conversation_id IN (`+oldestFilter+`)`, excess); err != nil {
		return 0, fmt.Errorf("deleting excess tool calls: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_skills WHERE conversation_id IN (`+oldestFilter+`)`, excess); err != nil {
		return 0, fmt.Errorf("deleting excess skill usages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_stats WHERE conversation_id IN (`+oldestFilter+`)`, excess); err != nil {
		return 0, fmt.Errorf("deleting excess conversation stats: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE conversation_id IN (`+oldestFilter+`)`, excess); err != nil {
		return 0, fmt.Errorf("deleting excess messages: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`DELETE FROM conversations WHERE id IN (`+oldestFilter+`)`, excess)
	if err != nil {
		return 0, fmt.Errorf("deleting excess conversations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing prune-by-count transaction: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// TelemetrySummary holds aggregated telemetry for the summary endpoint.
type TelemetrySummary struct {
	ByModel []ModelCostSummary  `json:"by_model"`
	ByTool  []ToolUsageSummary  `json:"by_tool"`
	BySkill []SkillUsageSummary `json:"by_skill"`
}

// ModelCostSummary aggregates cost/token data per model.
type ModelCostSummary struct {
	Model        string  `db:"model"          json:"model"`
	Provider     string  `db:"provider"       json:"provider"`
	TotalCost    float64 `db:"total_cost"     json:"total_cost"`
	MessageCount int     `db:"message_count"  json:"message_count"`
	TotalPrompt  int     `db:"total_prompt"   json:"total_tokens_prompt"`
	TotalCompl   int     `db:"total_completion" json:"total_tokens_completion"`
	TotalCached  int     `db:"total_cached"   json:"total_tokens_cached"`
}

// ToolUsageSummary aggregates tool call data per tool.
type ToolUsageSummary struct {
	ToolName    string  `db:"tool_name"     json:"tool_name"`
	ServerName  string  `db:"server_name"   json:"server_name"`
	CallCount   int     `db:"call_count"    json:"call_count"`
	ErrorCount  int     `db:"error_count"   json:"error_count"`
	AvgDuration float64 `db:"avg_duration"  json:"avg_duration_ms"`
}

// SkillUsageSummary aggregates skill usage data per skill.
type SkillUsageSummary struct {
	SkillName  string `db:"skill_name"  json:"skill_name"`
	MatchCount int    `db:"match_count" json:"match_count"`
	MatchTypes string `db:"match_types" json:"match_types"`
}

// GetTelemetrySummary returns aggregated telemetry data, optionally filtered by time range.
func (s *SQLiteMemoryStore) GetTelemetrySummary(ctx context.Context, since, until *time.Time) (*TelemetrySummary, error) {
	summary := &TelemetrySummary{}

	timeFilter, args := buildTimeFilter(since, until)

	// Cost by model.
	modelQuery := `SELECT model, provider, SUM(cost) AS total_cost, COUNT(*) AS message_count,
	               SUM(tokens_prompt) AS total_prompt, SUM(tokens_completion) AS total_completion,
	               SUM(tokens_cached) AS total_cached
	               FROM messages WHERE role = 'assistant' AND model != ''` + timeFilter + `
	               GROUP BY model, provider ORDER BY total_cost DESC`
	if err := s.db.SelectContext(ctx, &summary.ByModel, modelQuery, args...); err != nil {
		return nil, fmt.Errorf("querying model summary: %w", err)
	}

	// Tool call frequency.
	toolQuery := `SELECT tool_name, server_name, COUNT(*) AS call_count,
	              SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END) AS error_count,
	              AVG(duration_ms) AS avg_duration
	              FROM tool_calls WHERE 1=1` + timeFilter + `
	              GROUP BY tool_name, server_name ORDER BY call_count DESC`
	if err := s.db.SelectContext(ctx, &summary.ByTool, toolQuery, args...); err != nil {
		return nil, fmt.Errorf("querying tool summary: %w", err)
	}

	// Skill usage frequency.
	skillQuery := `SELECT skill_name, COUNT(*) AS match_count,
	               GROUP_CONCAT(DISTINCT match_type) AS match_types
	               FROM message_skills WHERE 1=1` + timeFilter + `
	               GROUP BY skill_name ORDER BY match_count DESC`
	if err := s.db.SelectContext(ctx, &summary.BySkill, skillQuery, args...); err != nil {
		return nil, fmt.Errorf("querying skill summary: %w", err)
	}

	return summary, nil
}

// buildTimeFilter creates a SQL time filter clause and args for since/until parameters.
func buildTimeFilter(since, until *time.Time) (string, []any) {
	var filter string
	var args []any
	if since != nil {
		filter += ` AND created_at >= ?`
		args = append(args, *since)
	}
	if until != nil {
		filter += ` AND created_at <= ?`
		args = append(args, *until)
	}
	return filter, args
}

func (s *SQLiteMemoryStore) Close() error {
	return s.db.Close()
}

// ---------------------------------------------------------------------------
// ActiveChannelStore implementation
// ---------------------------------------------------------------------------

func (s *SQLiteMemoryStore) GetActiveChannel(ctx context.Context, adapterKey string) (string, error) {
	var name string
	err := s.db.GetContext(ctx, &name,
		`SELECT channel_name FROM active_channels WHERE adapter_key = ?`, adapterKey)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("getting active channel for %q: %w", adapterKey, err)
	}
	return name, nil
}

func (s *SQLiteMemoryStore) SetActiveChannel(ctx context.Context, adapterKey, channelName string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO active_channels (adapter_key, channel_name, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(adapter_key) DO UPDATE SET channel_name = excluded.channel_name, updated_at = CURRENT_TIMESTAMP`,
		adapterKey, channelName)
	if err != nil {
		return fmt.Errorf("setting active channel for %q: %w", adapterKey, err)
	}
	return nil
}

func (s *SQLiteMemoryStore) ClearActiveChannel(ctx context.Context, adapterKey string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM active_channels WHERE adapter_key = ?`, adapterKey)
	if err != nil {
		return fmt.Errorf("clearing active channel for %q: %w", adapterKey, err)
	}
	return nil
}

func (s *SQLiteMemoryStore) ListActiveChannels(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT adapter_key, channel_name FROM active_channels`)
	if err != nil {
		return nil, fmt.Errorf("listing active channels: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]string)
	for rows.Next() {
		var key, name string
		if err := rows.Scan(&key, &name); err != nil {
			return nil, fmt.Errorf("scanning active channel row: %w", err)
		}
		result[key] = name
	}
	return result, rows.Err()
}
