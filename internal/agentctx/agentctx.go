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
	keyExecMark       ctxKey = "exec_mark"
)

// ExecMark labels every audit event produced by a turn that ran under a
// non-live execution policy (a dry-run preview or an eval sample), so the
// record stays complete while the view can stay quiet.
//
// It travels in the context rather than as a parameter because audit events
// are emitted from a dozen places in the turn — including helpers that never
// see the policy — and a single overlay at the emit site is the only way to
// mark all of them without threading the policy everywhere.
type ExecMark struct {
	// Agent is the variant-scoped pseudo-identity written to the event's
	// agent field, e.g. "pamela#dryrun" or "pamela#eval:candidate". The "#"
	// is rejected by resource-name validation, so it can never collide with a
	// real agent, and exact-match agent queries exclude these events for free.
	Agent string
	// Source is the event source, e.g. "dryrun" or "eval". Exact-match
	// filterable, and excludable via the audit exclude_source filter.
	Source string
	// Summary requests reduced emission (lifecycle and errors only) instead of
	// the default full live-turn semantics.
	Summary bool
}

// WithExecMark returns a context carrying the audit overlay for a non-live turn.
func WithExecMark(ctx context.Context, m *ExecMark) context.Context {
	return context.WithValue(ctx, keyExecMark, m)
}

// ExecMarkFrom extracts the audit overlay, or nil for an ordinary live turn.
func ExecMarkFrom(ctx context.Context) *ExecMark {
	v, _ := ctx.Value(keyExecMark).(*ExecMark)
	return v
}

// SkillSummary carries lightweight metadata about the skill driving the
// current session. Used by the supervisor to understand *why* a tool call
// is being made, especially for scheduled skill invocations.
type SkillSummary struct {
	Name         string
	Description  string
	Body         string // skill body (markdown); truncated at render time
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
