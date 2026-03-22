package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// StoredMessage represents a message persisted in the memory store.
type StoredMessage struct {
	ID             int64     `db:"id"`
	ConversationID string    `db:"conversation_id"`
	Role           string    `db:"role"`
	Content        string    `db:"content"`
	TokensUsed     int       `db:"tokens_used"`
	Cost           float64   `db:"cost"`
	CreatedAt      time.Time `db:"created_at"`
}

// MemoryStore defines the interface for conversation persistence.
type MemoryStore interface {
	GetOrCreateConversation(ctx context.Context, adapter, externalID string) (string, error)
	// GetOrCreateConversationByID ensures a conversation row exists for the given
	// convID without requiring a real adapter/externalID pair. Used by the
	// scheduler for isolated sessions that are not tied to a chat channel.
	GetOrCreateConversationByID(ctx context.Context, convID string) error
	AddMessage(ctx context.Context, convID string, msg StoredMessage) error
	GetMessages(ctx context.Context, convID string, limit int) ([]StoredMessage, error)
	Close() error
}

// SQLiteMemoryStore implements MemoryStore using SQLite.
type SQLiteMemoryStore struct {
	db *sqlx.DB
}

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

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	return &SQLiteMemoryStore{db: db}, nil
}

// NewInMemoryStore creates an in-memory SQLite store (for testing).
func NewInMemoryStore() (*SQLiteMemoryStore, error) {
	db, err := sqlx.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
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

func (s *SQLiteMemoryStore) GetOrCreateConversationByID(ctx context.Context, convID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO conversations (id, adapter, external_id) VALUES (?, ?, ?)`,
		convID, "sched", convID,
	)
	if err != nil {
		return fmt.Errorf("creating conversation: %w", err)
	}
	return nil
}

func (s *SQLiteMemoryStore) AddMessage(ctx context.Context, convID string, msg StoredMessage) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (conversation_id, role, content, tokens_used, cost) VALUES (?, ?, ?, ?, ?)`,
		convID, msg.Role, msg.Content, msg.TokensUsed, msg.Cost,
	)
	if err != nil {
		return fmt.Errorf("adding message: %w", err)
	}
	return nil
}

func (s *SQLiteMemoryStore) GetMessages(ctx context.Context, convID string, limit int) ([]StoredMessage, error) {
	var messages []StoredMessage
	err := s.db.SelectContext(ctx, &messages,
		`SELECT id, conversation_id, role, content, tokens_used, cost, created_at
		 FROM messages WHERE conversation_id = ? ORDER BY created_at ASC LIMIT ?`,
		convID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("getting messages: %w", err)
	}
	return messages, nil
}

func (s *SQLiteMemoryStore) Close() error {
	return s.db.Close()
}
