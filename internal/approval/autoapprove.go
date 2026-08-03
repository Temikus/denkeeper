package approval

import (
	"context"
	"time"
)

// AutoApproveScope identifies where an auto-approve rule originated.
type AutoApproveScope string

const (
	// ScopeSession is an ephemeral in-memory rule scoped to a conversation.
	ScopeSession AutoApproveScope = "session"

	// ScopePermanent is a persisted rule scoped to an agent (survives restarts).
	ScopePermanent AutoApproveScope = "permanent"

	// ScopeConfig is a TOML-declared rule scoped to an agent
	// (`[[agents]] auto_approve_tools`). Config rules live in memory on the
	// Manager, are replaced wholesale by the config-load path (startup and
	// hot-reload), and cannot be created or removed at runtime — they survive
	// DB resets and ship with the agent definition.
	ScopeConfig AutoApproveScope = "config"
)

// AutoApproveRule is a rule that allows a specific tool to bypass the approval
// workflow for a given agent. Session-scoped rules are held in memory and
// expire after a TTL; permanent rules are persisted in SQLite; config-scoped
// rules are held in memory and sourced from the TOML config (no ID, no
// timestamps — there is nothing to address for deletion).
//
// CreatedAt is `omitzero` because the in-memory scopes (session, config) have
// no creation timestamp: without it the year-1 zero time would serialize as if
// it were a real date.
type AutoApproveRule struct {
	ID             string           `db:"id"              json:"id"`
	AgentName      string           `db:"agent_name"      json:"agent_name"`
	ToolName       string           `db:"tool_name"       json:"tool_name"`
	Scope          AutoApproveScope `db:"scope"           json:"scope"`
	ConversationID string           `db:"conversation_id" json:"conversation_id,omitempty"`
	ExpiresAt      *time.Time       `db:"-"               json:"expires_at,omitempty"`
	CreatedAt      time.Time        `db:"created_at"      json:"created_at,omitzero"`
	CreatedBy      string           `db:"created_by"      json:"created_by"`
}

// AutoApproveStore defines the persistence interface for permanent auto-approve rules.
type AutoApproveStore interface {
	CreateAutoApproveRule(ctx context.Context, rule AutoApproveRule) (string, error)
	DeleteAutoApproveRule(ctx context.Context, id string) error
	ListAutoApproveRules(ctx context.Context, agentName string) ([]AutoApproveRule, error)
	MatchAutoApproveRule(ctx context.Context, agentName, toolName string) (*AutoApproveRule, error)
}
