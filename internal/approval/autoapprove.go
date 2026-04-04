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

	// ScopeConfig is reserved for future TOML-based policy rules.
	// ScopeConfig AutoApproveScope = "config"
)

// AutoApproveRule is a rule that allows a specific tool to bypass the approval
// workflow for a given agent. Session-scoped rules are held in memory;
// permanent rules are persisted in SQLite.
type AutoApproveRule struct {
	ID             string           `db:"id"              json:"id"`
	AgentName      string           `db:"agent_name"      json:"agent_name"`
	ToolName       string           `db:"tool_name"       json:"tool_name"`
	Scope          AutoApproveScope `db:"scope"           json:"scope"`
	ConversationID string           `db:"conversation_id" json:"conversation_id,omitempty"`
	CreatedAt      time.Time        `db:"created_at"      json:"created_at"`
	CreatedBy      string           `db:"created_by"      json:"created_by"`
}

// AutoApproveStore defines the persistence interface for permanent auto-approve rules.
type AutoApproveStore interface {
	CreateAutoApproveRule(ctx context.Context, rule AutoApproveRule) (string, error)
	DeleteAutoApproveRule(ctx context.Context, id string) error
	ListAutoApproveRules(ctx context.Context, agentName string) ([]AutoApproveRule, error)
	MatchAutoApproveRule(ctx context.Context, agentName, toolName string) (*AutoApproveRule, error)
}
