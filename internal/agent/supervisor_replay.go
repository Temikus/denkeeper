package agent

import (
	"context"

	"github.com/Temikus/denkeeper/internal/llm"
)

// DefaultSupervisorContextMessages is how many recent messages a review sees
// when supervisor_context_messages is unset.
const DefaultSupervisorContextMessages = defaultSupervisorContextMessages

// DeciderReplayInput is what a past supervisor review can be rebuilt from.
// Tool description, server guidance and skill context are not recorded in the
// audit event, so a replayed state is thinner than the live one.
type DeciderReplayInput struct {
	Agent     string
	Tool      string
	Arguments string
	Recent    []StoredMessage
}

// ReplaySupervisorDecider scores a past tool call with the live stage's state
// rendering and questions. It audits nothing and bills sessionID only if d has
// a cost tracker.
func ReplaySupervisorDecider(ctx context.Context, d *llm.Decider, sessionID string, in DeciderReplayInput) (*llm.DecisionResponse, error) {
	rin := &supervisorReviewInput{agent: in.Agent, tool: in.Tool, arguments: in.Arguments}
	rin.setRecent(in.Recent)
	return d.Decide(ctx, sessionID, rin.deciderState(), supervisorDeciderQuestions(nil))
}

// SupervisorDeciderVerdict applies the decider stage's threshold policy and
// returns "APPROVE", "DENY" or "ESCALATE" with the reason.
func SupervisorDeciderVerdict(answers map[string]llm.Answer, approveAt, denyAt float64) (string, string) {
	d, reason := decideSupervisorOutcome(answers, approveAt, denyAt)
	return string(d), reason
}

// SupervisorErrorCause classifies a failed review as the audit trail does:
// cost_limit, timeout, too_large or provider_error.
func SupervisorErrorCause(err error) string { return supervisorErrorCause(err) }
