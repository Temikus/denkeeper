// Package audit provides a unified audit trail for all agent activity.
package audit

import (
	"context"
	"time"
)

// Event categories.
const (
	CategoryToolCall = "tool_call"
	CategorySkill    = "skill"
	CategoryChannel  = "channel"
	CategoryApproval = "approval"
	CategorySchedule = "schedule"
	CategoryLLM      = "llm"
	CategoryConfig   = "config"
	CategorySession  = "session"
	CategoryMCP      = "mcp"
)

// Event statuses.
const (
	StatusOK      = "ok"
	StatusError   = "error"
	StatusPending = "pending"
	StatusDenied  = "denied"
)

// Event represents a single audit log entry.
type Event struct {
	ID             int64     `json:"id"`
	Timestamp      time.Time `json:"timestamp"`
	Category       string    `json:"category"`
	Action         string    `json:"action"`
	Agent          string    `json:"agent"`
	Summary        string    `json:"summary"`
	Detail         string    `json:"detail"`
	Status         string    `json:"status"`
	DurationMs     int64     `json:"duration_ms"`
	Source         string    `json:"source"`
	ConversationID string    `json:"conversation_id"`
}

// ListOpts controls filtering and pagination for audit event queries.
type ListOpts struct {
	Category string
	Agent    string
	Status   string
	Source   string
	Search   string
	Since    *time.Time
	Until    *time.Time
	Limit    int
	Offset   int
}

// Stats holds aggregate counts for the audit log dashboard.
type Stats struct {
	Total          int            `json:"total"`
	ByCategory     map[string]int `json:"by_category"`
	ByStatus       map[string]int `json:"by_status"`
	EventsLastHour int            `json:"events_last_hour"`
}

// ListResult wraps a paginated list response.
type ListResult struct {
	Events []Event `json:"events"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

// Emitter is the interface for emitting audit events.
// Implementations must be safe for concurrent use.
type Emitter interface {
	Emit(ctx context.Context, event Event)
}

// Store persists and queries audit events.
type Store interface {
	Insert(ctx context.Context, event Event) error
	InsertBatch(ctx context.Context, events []Event) error
	List(ctx context.Context, opts ListOpts) ([]Event, int, error)
	Stats(ctx context.Context, since *time.Time) (*Stats, error)
	PruneBefore(ctx context.Context, before time.Time) (int, error)
	Close() error
}
