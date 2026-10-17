// Package task stores durable tasks that den agents, external MCP clients and
// the operator hand to each other. Design: design/tasks-mvp.md.
package task

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task statuses.
const (
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusReview     = "review"
	StatusBlocked    = "blocked"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// Event kinds recorded in a task's thread.
const (
	EventCreated  = "created"
	EventComment  = "comment"
	EventClaimed  = "claimed"
	EventReleased = "released"
	EventStatus   = "status"
	EventAssigned = "assigned"
	EventSubtask  = "subtask"
	EventEdited   = "edited"
)

// Member ID prefixes. A member ID is "agent:<name>", "ext:<key name>" or Human,
// so a den agent and an API key with the same name never collide.
const (
	AgentPrefix    = "agent:"
	ExternalPrefix = "ext:"
	Human          = "human"
)

// Input limits.
const (
	MaxTitleLen  = 200
	MaxBodyLen   = 20000
	MaxReasonLen = 500
)

// Default subtask limits.
const (
	DefaultMaxDepth    = 3
	DefaultMaxChildren = 20
)

// Task is one row of the tasks table plus its subtask counts.
type Task struct {
	ID            int64      `db:"id" json:"id"`
	Title         string     `db:"title" json:"title"`
	Body          string     `db:"body" json:"body"`
	Status        string     `db:"status" json:"status"`
	Assignee      string     `db:"assignee" json:"assignee"`
	CreatedBy     string     `db:"created_by" json:"created_by"`
	Reviewer      string     `db:"reviewer" json:"reviewer"`
	ParentID      *int64     `db:"parent_id" json:"parent_id,omitempty"`
	Depth         int        `db:"depth" json:"depth"`
	Holder        string     `db:"holder" json:"holder"`
	HeldAt        *time.Time `db:"held_at" json:"held_at,omitempty"`
	BlockedReason string     `db:"blocked_reason" json:"blocked_reason,omitempty"`
	CreatedAt     time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at" json:"updated_at"`
	ClosedAt      *time.Time `db:"closed_at" json:"closed_at,omitempty"`
	ChildrenTotal int        `db:"children_total" json:"children_total"`
	ChildrenDone  int        `db:"children_done" json:"children_done"`
}

// Key is the display ID, e.g. "DK-131".
func (t *Task) Key() string { return Key(t.ID) }

// Closed reports whether the task is done or cancelled.
func (t *Task) Closed() bool { return IsClosed(t.Status) }

// Key formats a task ID for display.
func Key(id int64) string { return fmt.Sprintf("DK-%d", id) }

// IsClosed reports whether status is terminal.
func IsClosed(status string) bool { return status == StatusDone || status == StatusCancelled }

// ValidStatus reports whether s is a known status.
func ValidStatus(s string) bool {
	switch s {
	case StatusTodo, StatusInProgress, StatusReview, StatusBlocked, StatusDone, StatusCancelled:
		return true
	}
	return false
}

// Event is one row of a task's thread.
type Event struct {
	ID     int64     `db:"id" json:"id"`
	TaskID int64     `db:"task_id" json:"task_id"`
	At     time.Time `db:"at" json:"at"`
	Actor  string    `db:"actor" json:"actor"`
	Kind   string    `db:"kind" json:"kind"`
	Body   string    `db:"body" json:"body"`
	Data   string    `db:"data" json:"data,omitempty"`
}

// Detail is a task with its thread, its subtasks and its parent.
type Detail struct {
	Task     Task    `json:"task"`
	Events   []Event `json:"events"`
	Children []Task  `json:"children"`
	Parent   *Task   `json:"parent,omitempty"`
}

// Member is someone a task can be assigned to.
type Member struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // "agent", "external" or "human"
	Label string `json:"label,omitempty"`
}

// AgentID returns the member ID for a den agent.
func AgentID(name string) string { return AgentPrefix + name }

// ExternalID returns the member ID for an API key name.
func ExternalID(keyName string) string { return ExternalPrefix + keyName }

// MemberName strips the prefix from a member ID.
func MemberName(id string) string {
	if n, ok := strings.CutPrefix(id, AgentPrefix); ok {
		return n
	}
	if n, ok := strings.CutPrefix(id, ExternalPrefix); ok {
		return n
	}
	return id
}

// ErrNotFound is returned when a task does not exist.
var ErrNotFound = errors.New("task not found")

// ConflictError is returned when a claim or update loses to another member.
// Message is written for an agent to act on: it names the holder and what to
// do instead, and never invites a retry.
type ConflictError struct {
	TaskID  int64
	Holder  string
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

// RuleError is returned when the actor may not make the change, or the
// change breaks a task rule (depth, review, missing reason).
type RuleError struct{ Message string }

func (e *RuleError) Error() string { return e.Message }

func ruleErr(format string, args ...any) error {
	return &RuleError{Message: fmt.Sprintf(format, args...)}
}
