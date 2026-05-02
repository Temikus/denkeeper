// Package agentctx defines context keys for adapter routing information
// that flows through the agent pipeline. Both agent and configmcp import
// this package to set and extract routing context without coupling to each other.
package agentctx

import "context"

type ctxKey string

const (
	keyAdapter        ctxKey = "adapter"
	keyExternalID     ctxKey = "external_id"
	keyConversationID ctxKey = "conversation_id"
	keySkillContext   ctxKey = "skill_context"
)

// SkillSummary carries lightweight metadata about the skill driving the
// current session. Used by the supervisor to understand *why* a tool call
// is being made, especially for scheduled skill invocations.
type SkillSummary struct {
	Name         string
	Description  string
	IsScheduled  bool
	ScheduleName string
}

// WithAdapter returns a context carrying the adapter name (e.g. "telegram", "ws").
func WithAdapter(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, keyAdapter, name)
}

// Adapter extracts the adapter name, or "" if unset.
func Adapter(ctx context.Context) string {
	v, _ := ctx.Value(keyAdapter).(string)
	return v
}

// WithExternalID returns a context carrying the platform-specific message ID.
func WithExternalID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyExternalID, id)
}

// ExternalID extracts the external message ID, or "" if unset.
func ExternalID(ctx context.Context) string {
	v, _ := ctx.Value(keyExternalID).(string)
	return v
}

// WithConversationID returns a context carrying the conversation ID.
func WithConversationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyConversationID, id)
}

// ConversationID extracts the conversation ID, or "" if unset.
func ConversationID(ctx context.Context) string {
	v, _ := ctx.Value(keyConversationID).(string)
	return v
}

// WithSkillContext returns a context carrying skill metadata for supervisor review.
func WithSkillContext(ctx context.Context, s *SkillSummary) context.Context {
	return context.WithValue(ctx, keySkillContext, s)
}

// SkillContext extracts the skill summary, or nil if unset.
func SkillContext(ctx context.Context) *SkillSummary {
	v, _ := ctx.Value(keySkillContext).(*SkillSummary)
	return v
}
