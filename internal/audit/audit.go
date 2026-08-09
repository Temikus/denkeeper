// Package audit provides a unified audit trail for all agent activity.
package audit

import (
	"context"
	"strings"
	"time"
)

// ParseFilterList normalizes multi-valued filter input into the union form
// ListOpts.Categories / ListOpts.Statuses expect. Each input may itself be
// comma-separated, so both `?category=llm,tool_call` and the repeated
// `?category=llm&category=tool_call` spelling arrive here as the same set.
// Blanks are dropped and duplicates collapsed, so a lone "" (the dashboard's
// "All" chip) yields nil — i.e. no filter.
func ParseFilterList(values ...string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, dup := seen[part]; dup {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

// Event categories.
const (
	CategoryToolCall   = "tool_call"
	CategorySkill      = "skill"
	CategoryChannel    = "channel"
	CategoryApproval   = "approval"
	CategorySchedule   = "schedule"
	CategoryLLM        = "llm"
	CategoryConfig     = "config"
	CategorySession    = "session"
	CategoryMCP        = "mcp"
	CategorySafety     = "safety"
	CategorySupervisor = "supervisor"
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
//
// Categories and Statuses are unions: an event matches when it carries any one
// of the listed values. Empty (or nil) means "no filter on this field".
type ListOpts struct {
	Categories []string
	Agent      string
	Statuses   []string
	Source     string
	Search     string
	Since      *time.Time
	Until      *time.Time
	Limit      int
	Offset     int
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
