package approval

import (
	"context"
	"errors"
	"time"
)

// Status represents the lifecycle state of an approval request.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
	StatusExpired  Status = "expired"
)

// ActionKind categorises what kind of action is awaiting approval.
type ActionKind string

const (
	// ActionKindUserUpdate is a request to update the agent's USER.md persona file.
	ActionKindUserUpdate ActionKind = "user_update"

	// ActionKindCreateSkill is a request to create a new skill file in the agent's skills directory.
	ActionKindCreateSkill ActionKind = "create_skill"

	// ActionKindModifySchedule is a request to register a new schedule entry at runtime.
	ActionKindModifySchedule ActionKind = "modify_schedule"
)

// Request is the persisted record of a pending or resolved approval.
type Request struct {
	ID      string     `db:"id"      json:"id"`
	AgentName string   `db:"agent_name" json:"agent_name"`
	Kind    ActionKind `db:"kind"    json:"kind"`
	Status  Status     `db:"status"  json:"status"`

	// Summary is a human-readable one-liner shown in the approval UI.
	Summary string `db:"summary" json:"summary"`

	// Payload is the content to apply when approved (e.g. full USER.md text).
	Payload string `db:"payload" json:"payload"`

	// CallbackData is the base prefix embedded in Telegram inline button data.
	// Format: "appr:{id}" — buttons append ":approve" or ":deny".
	CallbackData string `db:"callback_data" json:"callback_data,omitempty"`

	// ExternalID is the adapter-level chat/channel ID to reply to after resolution.
	ExternalID string `db:"external_id" json:"external_id"`

	// AdapterName identifies which adapter to use for confirmation messages.
	AdapterName string `db:"adapter_name" json:"adapter_name"`

	// ConversationID links this approval to the engine conversation that created it.
	ConversationID string `db:"conversation_id" json:"conversation_id"`

	CreatedAt  time.Time  `db:"created_at"  json:"created_at"`
	ResolvedAt *time.Time `db:"resolved_at" json:"resolved_at,omitempty"`

	// ResolvedBy records who resolved the approval: "telegram", "api", or "expired".
	ResolvedBy string `db:"resolved_by" json:"resolved_by,omitempty"`
}

// Store defines the persistence interface for approval requests.
// In-memory action closures are managed separately by the Registry, since
// closures cannot be serialised. On restart, any pending rows are expired by
// ExpirePending so stale entries are never silently lost.
type Store interface {
	// Create persists a new approval request and returns the assigned ID.
	Create(ctx context.Context, req Request) (string, error)

	// Get fetches a single approval by ID. Returns ErrNotFound if absent.
	Get(ctx context.Context, id string) (*Request, error)

	// List returns approvals filtered by status. Pass an empty string for all.
	List(ctx context.Context, status Status) ([]Request, error)

	// Resolve transitions the status of a pending approval.
	// Returns ErrNotFound if the ID does not exist.
	// Returns ErrAlreadyResolved if the approval is not currently pending.
	Resolve(ctx context.Context, id string, status Status, resolvedBy string) error

	// ResolveByCallbackPrefix looks up by callback_data prefix, then resolves.
	// Returns ErrNotFound if no pending approval matches.
	ResolveByCallbackPrefix(ctx context.Context, prefix string, status Status, resolvedBy string) (*Request, error)

	// ExpirePending marks all pending approvals as expired. Call at startup.
	// Returns the number of rows affected.
	ExpirePending(ctx context.Context) (int, error)

	Close() error
}

// Sentinel errors returned by Store implementations.
var (
	ErrNotFound        = errors.New("approval: not found")
	ErrAlreadyResolved = errors.New("approval: already resolved")
)
