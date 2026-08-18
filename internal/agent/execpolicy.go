package agent

import (
	"fmt"
	"time"

	"github.com/Temikus/denkeeper/internal/agentctx"
	"github.com/Temikus/denkeeper/internal/llm"
)

// ExecKind labels why a turn is running under an ExecPolicy. The zero value
// is an ordinary live turn; any other value doubles as the audit source.
type ExecKind string

const (
	// ExecLive is the absence of a policy: a real turn with real persistence.
	ExecLive ExecKind = ""
	// ExecDryRun is an operator-triggered preview ("Test now").
	ExecDryRun ExecKind = "dryrun"
	// ExecEval is one sample of an eval run.
	ExecEval ExecKind = "eval"
)

// Audit emission modes for a policy turn.
const (
	// AuditFull emits full live-turn semantics — the record genuinely
	// represents what happened. This is the default.
	AuditFull = "full"
	// AuditSummary emits lifecycle events and errors only.
	AuditSummary = "summary"
)

// suppressedResult is the synthetic tool result returned in place of a write
// the policy refused to execute. It is phrased so the model keeps planning
// against a plausible world instead of retrying the call.
const suppressedResultFmt = "[dry-run: write suppressed — %s not executed; assume success]"

// outcomeSuppressed is the ToolCallRecord outcome for a write the policy
// refused to execute. Distinct from the existing ok/rejected/failed/denied/
// cached set: nothing ran, and nothing is at fault.
const outcomeSuppressed = "suppressed"

// ExecPolicy is the per-request execution policy for a turn that must not
// touch the world: no writes, no persistence, no approvals, no adapters. It is
// resolved once by the caller and threaded through the turn alongside the
// tool-round budget — there is no engine-level mutable state, so a policy turn
// and a live turn can run concurrently on the same Engine.
//
// Tool execution is split by the *existing* idempotency signal
// (tool.Manager.IsIdempotent, built for within-turn memoization): idempotent
// tools run for real so the model sees a truthful world; everything else
// returns a suppression marker.
type ExecPolicy struct {
	// Kind selects the policy flavour and becomes the audit source.
	Kind ExecKind
	// Variant names the eval variant this sample belongs to (e.g. "candidate").
	// Empty for dry-runs and for the conventional incumbent.
	Variant string
	// Model overrides the agent's model for this turn only. Empty runs the
	// agent's live model. The override is applied by cloning the router
	// (llm.Router.WithModel) rather than mutating it, so a preview of a
	// candidate model cannot retarget a live turn already in flight.
	Model string
	// Provider overrides the agent's provider for this turn only, composed
	// with Model by the same clone (llm.Router.WithProvider). Empty runs the
	// agent's live provider. Safe to override freely because every agent
	// router registers every configured provider, so the clone is a pointer
	// swap rather than a rebuild.
	Provider string
	// ConvID is the in-flight conversation identity — "dryrun:{uuid}" or
	// "eval:{run}:{task}:{k}". It is used for cost tracking, audit grouping and
	// log correlation, and is never written to the conversations table.
	ConvID string
	// AsOf pins the clock for both date-injection points (the scheduled-message
	// header and the "## Current Date" prompt section), so a replay is
	// date-deterministic. Zero means "now".
	AsOf time.Time
	// HistoryFrom names a conversation whose recent messages are loaded
	// read-only as the context *preceding* this turn. Empty means a fresh turn
	// with no history. The message being answered is always appended after it,
	// so a caller replaying a stored conversation points here at the messages
	// before the one it re-runs, not at the whole thing.
	HistoryFrom string
	// History is context pinned by the caller and replayed verbatim as the
	// messages preceding this turn. It takes precedence over HistoryFrom.
	//
	// The two differ in *when* the context is chosen. HistoryFrom names a live
	// conversation and reads its most recent window at turn time, which is
	// right for "preview this against whatever the session looks like now".
	// History carries a snippet captured earlier, which is what a saved eval
	// task needs: the source conversation drifts (ClearMessages empties it,
	// retention prunes it) and its latest window is not the window that
	// preceded the saved message. A test case that silently re-scopes itself
	// between runs is not a test case.
	//
	// Only Role and Content are read; the remaining StoredMessage fields are
	// ignored by assembleMessages and may be left zero.
	History []StoredMessage
	// AuditMode is AuditFull (default) or AuditSummary.
	AuditMode string
}

// active reports whether p asks for non-live execution. Nil-safe: a nil
// policy is an ordinary live turn.
func (p *ExecPolicy) active() bool {
	return p != nil && p.Kind != ExecLive
}

// summaryAudit reports whether the policy asks for reduced audit emission.
func (p *ExecPolicy) summaryAudit() bool {
	return p != nil && p.AuditMode == AuditSummary
}

// auditAgent builds the variant-scoped pseudo-identity written to the agent
// field of every audit event this turn emits: "pamela#dryrun" for a preview,
// "pamela#eval:candidate" for an eval sample.
func (p *ExecPolicy) auditAgent(base string) string {
	if !p.active() {
		return base
	}
	if p.Variant != "" {
		return base + "#" + string(p.Kind) + ":" + p.Variant
	}
	return base + "#" + string(p.Kind)
}

// mark builds the audit overlay carried in the turn's context.
func (p *ExecPolicy) mark(base string) *agentctx.ExecMark {
	if !p.active() {
		return nil
	}
	return &agentctx.ExecMark{
		Agent:   p.auditAgent(base),
		Source:  string(p.Kind),
		Summary: p.summaryAudit(),
	}
}

// clock returns the policy's pinned time, falling back to fallback when the
// policy is inactive or carries no pinned time.
func (p *ExecPolicy) clock(fallback func() time.Time) func() time.Time {
	if !p.active() || p.AsOf.IsZero() {
		return fallback
	}
	asOf := p.AsOf
	return func() time.Time { return asOf }
}

// suppresses reports whether a call to the named tool must be replaced by a
// synthetic result rather than executed. Unknown tools are suppressed: the
// idempotency allowlist is the only "safe to execute" signal, and its default
// is deliberately false.
func (p *ExecPolicy) suppresses(name string, idempotent func(string) bool) bool {
	if !p.active() {
		return false
	}
	return idempotent == nil || !idempotent(name)
}

// turnRun carries the per-turn execution parameters resolved once in
// chatWithApproval and threaded down the tool loop: the effective tool-round
// budget, the execution policy, and the router the turn talks to. Bundling
// them keeps the loop signatures from growing a parameter per concern.
//
// router is resolved once at the top of the turn so every completion in the
// tool loop — including the wrap-up and nudge-retry rounds — reaches the same
// model. A turn that started on a candidate model must not silently finish on
// the live one.
//
// Invariant: router must be non-nil on any turnRun that reaches a completion
// path. chatWithApproval is the only production constructor and always sets
// it; a zero turnRun is fine for the tool-execution helpers (which read only
// policy) and will panic loudly if it ever reaches the router, which is the
// failure mode we want for a construction bug.
// stopGen is the engine's stop generation as it was when the turn began; the
// loop compares it against the live value at every step boundary (see
// Engine.RequestStop). Zero on a bare turnRun, which matches a fresh engine and
// so reads as "no stop requested" for the tool-execution helpers.
type turnRun struct {
	budget  turnToolBudget
	policy  *ExecPolicy
	router  *llm.Router
	stopGen uint64
}

// TurnResult is everything a caller needs from one turn executed outside the
// normal chat pipeline. The eval runner and the dry-run handlers store it
// where they want; nothing here has been persisted.
type TurnResult struct {
	// ConversationID is the in-flight identity the turn ran under.
	ConversationID string `json:"conversation_id"`
	// Prompt is the message text that was sent, after any header injection.
	Prompt string `json:"prompt"`
	// Response is the final assistant text.
	Response string `json:"response"`
	// ToolCalls are the calls this turn made, in execution order, carrying
	// arguments and results (in-memory only — nothing was written).
	ToolCalls []ToolCallRecord `json:"tool_calls"`
	// Rounds is the number of tool-call rounds the turn used.
	Rounds int `json:"rounds"`
	// StopReason is why the tool loop ended, as a machine-readable slug
	// ("repeated_calls", "max_rounds", "stop_requested"). Empty means the model
	// finished on its own — the ordinary case. It is the only non-textual
	// signal that a turn was cut short: the alternative is scraping the
	// "[engine: turn ended early — …]" marker out of the response body.
	StopReason string `json:"stop_reason,omitempty"`
	// Tokens and Cost cover the whole turn, accumulated across rounds.
	Tokens  llm.TokenUsage `json:"tokens"`
	CostUSD float64        `json:"cost_usd"`
	// Model and Provider identify what actually answered — read back from the
	// response, so an override that the provider silently redirected shows the
	// model that really ran rather than the one that was asked for.
	Model    string `json:"model"`
	Provider string `json:"provider"`
	// Upstream is the provider-reported serving upstream (OpenRouter's routed
	// provider), empty when the provider has no such concept.
	Upstream string `json:"upstream,omitempty"`
	// RequestedModel is the override the caller asked for, empty when the turn
	// ran the agent's live model. The pair is what lets a transcript say "this
	// is not your live model" without the UI having to know the agent config.
	RequestedModel string `json:"requested_model,omitempty"`
	// AsOf is the clock the turn ran under.
	AsOf time.Time `json:"as_of"`
	// DurationMs is wall-clock time for the whole turn.
	DurationMs int64 `json:"duration_ms"`
}

// suppressedToolResult renders the synthetic result for a suppressed call.
func suppressedToolResult(name string) string {
	return fmt.Sprintf(suppressedResultFmt, name)
}

// toolRounds returns the number of tool-call rounds represented by records —
// the highest 1-based round any call ran in, which is exactly the round count
// because every round executes at least one call.
func toolRounds(records []ToolCallRecord) int {
	n := 0
	for _, r := range records {
		if r.Round > n {
			n = r.Round
		}
	}
	return n
}
