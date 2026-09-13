package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
)

// Reply-guard signal slugs. These reach the audit detail and the operator
// notice, so they are machine-readable and stable.
const (
	signalRoleMarkup     = "role_markup"
	signalOversized      = "oversized_reply"
	signalNoToolCalls    = "no_tool_calls"
	signalOversizeTokens = "oversized_completion"
	signalLeakedToolCall = "leaked_tool_call"
)

// Reply-guard actions. Each signal carries one.
const (
	// GuardOff disables a signal: it is not evaluated at all.
	GuardOff = "off"
	// GuardWarn audits the trip and delivers the reply unchanged.
	GuardWarn = "warn"
	// GuardWithhold audits the trip and replaces the wire text with a notice.
	GuardWithhold = "withhold"
)

// Default reply-guard bounds. The byte cap is roughly four Telegram chunks and
// under half the adapter's own render limit (maxOutboundChunks *
// MessageChunkLimit = 35000), so a trip is a strong signal rather than a
// long-but-legitimate reply.
const (
	defaultMaxReplyBytes = 16000
	defaultExcerptBytes  = 200
)

// replyMarkupMarkers are role and tool-call markup fragments that must never
// appear in a final assistant reply as plain text. A model emitting one has
// leaked its own scaffolding instead of calling a tool. Matched
// case-insensitively; extend the list freely, it is read-only at runtime.
var replyMarkupMarkers = []string{
	"<rs_tool_calls",
	"<rs_tool ",
	"<tool_call>",
	"<tool_calls>",
	"<function_calls>",
	"<invoke name=",
	"<|im_start|>",
	"<|im_end|>",
	"<|assistant|>",
	"<|user|>",
	"<result>",
	"\n\nhuman:",
	"\n\nassistant:",
	"\n\nsystem:",
}

// ReplyGuard holds the resolved reply sanity guard settings for one engine.
// It is the agent-side mirror of config.ReplyGuardConfig; the agent package
// deliberately does not import config, so main.go translates.
//
// The zero value is a disabled guard, which is what every engine built without
// explicit wiring (tests, the reviewer engine) gets.
type ReplyGuard struct {
	Enabled bool
	// OnRoleMarkup, OnOversized and OnNoToolCalls are GuardOff/GuardWarn/
	// GuardWithhold. An unset action reads as GuardOff.
	OnRoleMarkup  string
	OnOversized   string
	OnNoToolCalls string
	// OnLeakedToolCall fires when the final text carries a tool call the
	// upstream failed to parse (llm.LeakedToolCallText). Unlike
	// OnNoToolCalls it fires even after successful tool rounds, since the
	// leak can happen on any round.
	OnLeakedToolCall string
	// MaxReplyBytes is the largest final reply, in bytes. Non-positive
	// disables the byte measure.
	MaxReplyBytes int
	// MaxCompletionTokens bounds provider-reported completion tokens.
	// Non-positive disables the token measure, which is the default: it
	// measures the same thing as bytes and doubles the false-positive surface.
	MaxCompletionTokens int
	// ExcerptBytes is how much of the raw reply reaches the audit detail.
	ExcerptBytes int
}

// ReplyGuardVerdict is the externally visible form of a guard trip, carried on
// TurnResult so a preview can show what the guard would have done.
type ReplyGuardVerdict struct {
	// Signals lists every signal that tripped, in evaluation order.
	Signals []string `json:"signals"`
	// Action is the strongest action any tripped signal asked for
	// ("warn" or "withhold").
	Action string `json:"action"`
	// Notice is the operator notice a live turn would have delivered in place
	// of the reply. Empty when the action is only "warn".
	Notice string `json:"notice,omitempty"`
}

// replyGuardResult is what one guard evaluation yields. The zero value means
// nothing tripped.
type replyGuardResult struct {
	// Signals lists every signal that tripped, in evaluation order.
	Signals []string
	// Action is the strongest action any tripped signal asked for.
	Action string
	// primary is the first signal that asked for GuardWithhold, or the first
	// signal overall when nothing withholds. It is what the notice names.
	primary string
	// Measures captured at evaluation time, for the audit detail.
	replyBytes       int
	completionTokens int
	toolCalls        int
}

// tripped reports whether any signal fired.
func (r replyGuardResult) tripped() bool { return len(r.Signals) > 0 }

// withholds reports whether the reply must not be delivered as written.
func (r replyGuardResult) withholds() bool { return r.Action == GuardWithhold }

// verdict renders the externally visible form, or nil when nothing tripped.
func (r replyGuardResult) verdict() *ReplyGuardVerdict {
	if !r.tripped() {
		return nil
	}
	v := &ReplyGuardVerdict{Signals: r.Signals, Action: r.Action}
	if r.withholds() {
		v.Notice = replyWithheldNotice(r)
	}
	return v
}

// evaluateReplyGuard runs the guard over one finished turn. It applies to
// schedule-driven turns only: a live user reads a broken reply and reacts,
// while a scheduled turn fires unattended.
//
// It is a pure function of its inputs — the caller decides what to do with the
// verdict — so a policy (dry-run/eval) turn can evaluate and report without
// substituting anything.
func evaluateReplyGuard(g ReplyGuard, msg adapter.IncomingMessage, content string, resp *llm.ChatResponse, records []ToolCallRecord) replyGuardResult {
	var out replyGuardResult
	if !g.Enabled || !msg.IsScheduled {
		return out
	}

	out.replyBytes = len(content)
	out.toolCalls = len(records)
	if resp != nil {
		out.completionTokens = resp.TokensUsed.Completion
	}

	// Order is most-diagnostic first: markup is unambiguous scaffolding
	// leakage, size is a strong hint, no-tool-calls is circumstantial.
	if g.OnRoleMarkup != GuardOff && containsReplyMarkup(content) {
		out.add(signalRoleMarkup, g.OnRoleMarkup)
	}
	if g.OnLeakedToolCall != GuardOff && llm.LeakedToolCallText(content) {
		out.add(signalLeakedToolCall, g.OnLeakedToolCall)
	}
	out.addOversized(g)
	// A schedule that named a skill expected that skill to do something. A
	// triggerless or ad-hoc scheduled message has no such expectation.
	if g.OnNoToolCalls != GuardOff && msg.SkillName != "" && out.toolCalls == 0 {
		out.add(signalNoToolCalls, g.OnNoToolCalls)
	}
	return out
}

// addOversized evaluates both size measures under the single OnOversized action.
func (r *replyGuardResult) addOversized(g ReplyGuard) {
	if g.OnOversized == GuardOff {
		return
	}
	if g.MaxReplyBytes > 0 && r.replyBytes > g.MaxReplyBytes {
		r.add(signalOversized, g.OnOversized)
	}
	if g.MaxCompletionTokens > 0 && r.completionTokens > g.MaxCompletionTokens {
		r.add(signalOversizeTokens, g.OnOversized)
	}
}

// add records one tripped signal and promotes the result's action. withhold
// beats warn: the strongest request wins, and the notice names the first
// signal that asked to withhold.
func (r *replyGuardResult) add(signal, action string) {
	r.Signals = append(r.Signals, signal)
	if r.primary == "" {
		r.primary = signal
	}
	if action == GuardWithhold {
		if r.Action != GuardWithhold {
			r.primary = signal
		}
		r.Action = GuardWithhold
		return
	}
	if r.Action == "" {
		r.Action = GuardWarn
	}
}

// containsReplyMarkup reports whether the text carries role or tool-call
// scaffolding. Matched case-insensitively over the whole reply.
func containsReplyMarkup(content string) bool {
	lower := strings.ToLower(content)
	for _, marker := range replyMarkupMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// replyWithheldNotice renders the one-line operator notice that goes over the
// wire in place of a withheld reply. It reuses the vocabulary of the
// "[engine: turn ended early — …]" marker so the two read as one family.
//
// It names the audit filter rather than an event id: audit.Emitter.Emit is
// fire-and-forget into a buffered channel and returns nothing, so no id exists
// until the batch writer has run.
func replyWithheldNotice(r replyGuardResult) string {
	return fmt.Sprintf(
		"[engine: reply withheld — %s; %d bytes, %d tool calls. See the audit log (safety / reply_guard).]",
		r.primary, r.replyBytes, r.toolCalls,
	)
}

// replyExcerpt returns the leading bytes of the raw reply for the audit
// detail, truncated on a rune boundary so the JSON stays valid UTF-8.
func replyExcerpt(content string, limit int) string {
	if limit <= 0 {
		limit = defaultExcerptBytes
	}
	if len(content) <= limit {
		return content
	}
	cut := content[:limit]
	// Back off to a rune boundary so the audit detail stays valid UTF-8.
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
