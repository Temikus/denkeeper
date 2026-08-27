package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
)

// L1 trace capture (design/eval-subsystem.md §4.2). A trace is the whole turn
// as the model saw it: the built system prompt post-skill-injection, the
// history window as sent, every tool call with its arguments and result, the
// final response, timings and usage.
//
// Traces hold the most sensitive data in the system, so live capture is
// opt-in ([eval] capture, default false). A turn running under an ExecPolicy
// builds its trace unconditionally — an eval sample's judge reads it — but the
// engine never persists one: a policy turn's isolation is structural, so the
// caller (the eval runner) decides where the trace goes.

// DefaultMaxTraceBytes is the per-trace payload cap when none is configured.
// Oldest rounds are dropped first when a trace exceeds it.
const DefaultMaxTraceBytes = 256 * 1024

// Trace sources. They mirror ExecKind, with "live" standing in for the zero
// value so a stored row never carries an empty discriminator. A dry-run turn's
// trace carries string(ExecDryRun) but gets no named constant: that trace only
// ever rides out on its TurnResult, so nothing stores it or filters on it.
const (
	TraceSourceLive = "live"
	TraceSourceEval = string(ExecEval)
)

// TraceSink persists captured turn traces. The eval store implements it;
// nil means capture is wired to nothing and the engine records nothing.
type TraceSink interface {
	SaveTrace(ctx context.Context, t TurnTrace) error
}

// TraceMessage is one message of the history window exactly as it went on the
// wire (the truncation notice included — it is part of what the model read).
type TraceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// TraceToolCall is one tool call with the payloads a stored ToolCallRecord
// deliberately drops: arguments and result are db:"-" on the record, and this
// is where they are finally written down.
type TraceToolCall struct {
	Tool       string `json:"tool"`
	Server     string `json:"server,omitempty"`
	Outcome    string `json:"outcome"`
	DurationMs int64  `json:"duration_ms"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

// TraceRound groups a turn's calls by the tool-call round they ran in, which
// is the unit truncation drops.
type TraceRound struct {
	Round     int             `json:"round"`
	ToolCalls []TraceToolCall `json:"tool_calls"`
}

// TraceTruncation records what the byte cap removed, so a reader never mistakes
// a trimmed trace for a short turn.
type TraceTruncation struct {
	DroppedRounds  int    `json:"dropped_rounds,omitempty"`
	DroppedHistory int    `json:"dropped_history,omitempty"`
	ClampedText    bool   `json:"clamped_text,omitempty"`
	Note           string `json:"note"`
}

// TracePayload is the JSON blob half of a trace: everything too big or too
// shapeless for a column.
type TracePayload struct {
	SystemPrompt string           `json:"system_prompt"`
	History      []TraceMessage   `json:"history"`
	Prompt       string           `json:"prompt"`
	Response     string           `json:"response"`
	Rounds       []TraceRound     `json:"rounds"`
	Truncation   *TraceTruncation `json:"truncation,omitempty"`
}

// TurnTrace is one recorded turn: metadata columns plus the payload blob.
type TurnTrace struct {
	ID             int64  `json:"id"`
	Agent          string `json:"agent"`
	ConversationID string `json:"conversation_id"`
	Source         string `json:"source"`
	Model          string `json:"model,omitempty"`
	Provider       string `json:"provider,omitempty"`
	// RequestedModel is the override the turn asked for, empty when it ran the
	// agent's live model.
	RequestedModel string `json:"requested_model,omitempty"`
	// Upstream is the provider-reported serving upstream, empty for providers
	// without the concept.
	Upstream   string         `json:"upstream,omitempty"`
	Rounds     int            `json:"rounds"`
	StopReason string         `json:"stop_reason,omitempty"`
	Tokens     llm.TokenUsage `json:"tokens"`
	CostUSD    float64        `json:"cost_usd"`
	LatencyMs  int64          `json:"latency_ms"`
	StartedAt  time.Time      `json:"started_at"`
	CreatedAt  time.Time      `json:"created_at"`
	// Truncated reports that the byte cap removed something; the detail is in
	// Payload.Truncation.
	Truncated bool `json:"truncated"`
	// Bytes is the encoded payload size after truncation.
	Bytes   int          `json:"bytes"`
	Payload TracePayload `json:"payload"`
	// encoded is the blob Bytes was measured from, kept so the store does not
	// marshal the same payload again. Unexported, so a trace that crossed a
	// JSON boundary simply re-encodes.
	encoded string
}

// EncodePayload serialises the payload blob for storage. buildTurnTrace has
// already encoded the payload once to size it against the cap, so the result
// is reused rather than marshalled a second time on the way to the store. Any
// code that mutates Payload after the trace is built must clear encoded.
func (t *TurnTrace) EncodePayload() (string, error) {
	if t.encoded != "" {
		return t.encoded, nil
	}
	b, err := json.Marshal(t.Payload)
	if err != nil {
		return "", fmt.Errorf("encoding trace payload: %w", err)
	}
	return string(b), nil
}

// DecodeTracePayload parses a stored payload blob.
func DecodeTracePayload(raw string) (TracePayload, error) {
	var p TracePayload
	if raw == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, fmt.Errorf("decoding trace payload: %w", err)
	}
	return p, nil
}

// groupTraceRounds folds tool records into rounds, preserving execution order
// within a round and round order across the turn.
func groupTraceRounds(records []ToolCallRecord) []TraceRound {
	if len(records) == 0 {
		return nil
	}
	rounds := make([]TraceRound, 0, 4)
	index := make(map[int]int, 4)
	for _, rec := range records {
		call := TraceToolCall{
			Tool:       rec.ToolName,
			Server:     rec.ServerName,
			Outcome:    rec.Outcome,
			DurationMs: rec.DurationMs,
			Arguments:  rec.Arguments,
			Result:     rec.Result,
			Error:      rec.ErrorMsg,
		}
		at, ok := index[rec.Round]
		if !ok {
			rounds = append(rounds, TraceRound{Round: rec.Round})
			at = len(rounds) - 1
			index[rec.Round] = at
		}
		rounds[at].ToolCalls = append(rounds[at].ToolCalls, call)
	}
	return rounds
}

// payloadSize is the encoded size the cap is measured against. An unencodable
// payload reports a size past any cap so truncation degrades to dropping
// content rather than storing something the store cannot write.
func payloadSize(p TracePayload) int {
	return jsonSize(p)
}

// jsonSize is the encoded byte count of v, or max int when it cannot encode.
func jsonSize(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return math.MaxInt
	}
	return len(b)
}

// truncateTracePayload trims p until its encoded form fits maxBytes, oldest
// first: tool-call rounds go before history, and history before the prompt and
// response text, because a trace exists first to say what the model was told
// and what it finally said. Reports whether anything was removed.
//
// The cap is hard: the truncation note rides inside the payload, so it is
// attached before the final measurement, and the last-resort text clamp works
// in encoded bytes rather than raw ones — JSON escaping can turn one byte of
// prompt into six.
//
// maxBytes <= 0 means the default cap; there is deliberately no "unbounded"
// value, since one pathological turn would otherwise be able to fill the table.
func truncateTracePayload(p TracePayload, maxBytes int) (TracePayload, bool) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxTraceBytes
	}
	// One full marshal to decide whether anything has to go; the drop loops
	// then track the size by subtracting what they remove, so a big trace is
	// not re-marshalled once per dropped round.
	size := payloadSize(p)
	if size <= maxBytes {
		return p, false
	}

	tr := TraceTruncation{}
	for len(p.Rounds) > 0 && size > maxBytes {
		size -= jsonSize(p.Rounds[0])
		p.Rounds = p.Rounds[1:]
		tr.DroppedRounds++
	}
	for len(p.History) > 0 && size > maxBytes {
		size -= jsonSize(p.History[0])
		p.History = p.History[1:]
		tr.DroppedHistory++
	}

	tr.Note = traceTruncationNote(tr, maxBytes)
	p.Truncation = &tr
	if payloadSize(p) <= maxBytes {
		return p, true
	}

	// Last resort: the prompt, the system prompt and the response are all that
	// is left. Halve the text budget until the encoded payload fits rather
	// than trusting one raw-byte guess.
	tr.ClampedText = true
	tr.Note = traceTruncationNote(tr, maxBytes)
	for budget := maxBytes; payloadSize(p) > maxBytes; budget /= 2 {
		p.SystemPrompt = clampTraceText(p.SystemPrompt, budget/2)
		p.Response = clampTraceText(p.Response, budget/4)
		p.Prompt = clampTraceText(p.Prompt, budget/8)
		if budget == 0 {
			// Everything clampable is already empty: only the note and the
			// envelope are left.
			break
		}
	}
	if over := payloadSize(p) - maxBytes; over > 0 {
		// A cap smaller than the note itself. The note is plain ASCII, so its
		// bytes are its encoded bytes; clamp it rather than break the cap.
		tr.Note = clampTraceText(tr.Note, max(len(tr.Note)-over, 0))
	}
	return p, true
}

// clampTraceText cuts s to at most n bytes on a rune boundary.
func clampTraceText(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func traceTruncationNote(tr TraceTruncation, maxBytes int) string {
	switch {
	case tr.ClampedText:
		return fmt.Sprintf("trace exceeded %d bytes: %d oldest round(s) and %d oldest history message(s) dropped, remaining text clamped",
			maxBytes, tr.DroppedRounds, tr.DroppedHistory)
	case tr.DroppedHistory > 0:
		return fmt.Sprintf("trace exceeded %d bytes: %d oldest round(s) and %d oldest history message(s) dropped",
			maxBytes, tr.DroppedRounds, tr.DroppedHistory)
	default:
		return fmt.Sprintf("trace exceeded %d bytes: %d oldest round(s) dropped", maxBytes, tr.DroppedRounds)
	}
}

// traceParams is what one finished turn hands the trace builder. Bundled
// rather than passed as eight arguments, and read-only: building a trace must
// not be able to change the turn it describes.
type traceParams struct {
	msg          adapter.IncomingMessage
	policy       *ExecPolicy
	prep         turnPrep
	resp         *llm.ChatResponse
	responseText string
	records      []ToolCallRecord
	stopReason   loopStopReason
	startedAt    time.Time
}

// buildTurnTrace assembles the turn's trace, or returns nil when this turn is
// not being recorded.
//
// A policy turn always builds one: an eval sample's judge reads the trace, and
// a dry-run transcript gets it for free. A live turn builds one only when the
// operator has turned capture on *and* a sink is wired — the default is off
// because a trace holds every byte the model saw.
//
// Response is the raw model text, not the wire text: on a withheld reply the
// point of the trace is to show what the model actually produced.
func (e *Engine) buildTurnTrace(p traceParams) *TurnTrace {
	_, capture, maxBytes := e.traceSettings()
	if !p.policy.active() && (!capture || !e.hasTraceSink()) {
		return nil
	}

	source := TraceSourceLive
	requested := ""
	if p.policy.active() {
		source = string(p.policy.Kind)
		requested = p.policy.Model
	}

	payload := TracePayload{
		SystemPrompt: p.prep.sysResult.prompt,
		History:      traceHistory(p.prep.llmMessages, p.msg.Text),
		Prompt:       p.msg.Text,
		Response:     p.responseText,
		Rounds:       groupTraceRounds(p.records),
	}
	payload, truncated := truncateTracePayload(payload, maxBytes)

	t := &TurnTrace{
		Agent:          e.name,
		ConversationID: p.prep.convID,
		Source:         source,
		Provider:       e.router.DefaultProvider(),
		RequestedModel: requested,
		Rounds:         toolRounds(p.records),
		StopReason:     p.stopReason.slug(),
		StartedAt:      p.startedAt,
		CreatedAt:      e.now(),
		LatencyMs:      e.now().Sub(p.startedAt).Milliseconds(),
		Truncated:      truncated,
		Payload:        payload,
	}
	if p.resp != nil {
		t.Model = p.resp.Model
		t.Upstream = p.resp.Upstream
		t.Tokens = p.resp.TokensUsed
		t.CostUSD = p.resp.CostUSD
	}
	if blob, err := json.Marshal(payload); err == nil {
		t.encoded = string(blob)
		t.Bytes = len(blob)
	}
	return t
}

// hasTraceSink reports whether a sink is wired.
func (e *Engine) hasTraceSink() bool {
	e.traceMu.RLock()
	defer e.traceMu.RUnlock()
	return e.traceSink != nil
}

// saveTurnTrace persists a live turn's trace. A policy turn is skipped
// deliberately: its isolation is structural, so the engine writes nothing and
// the caller (the eval runner) decides where its trace goes.
//
// Failure is logged and swallowed — a turn that answered the user must not be
// failed by a bookkeeping write.
func (e *Engine) saveTurnTrace(ctx context.Context, t *TurnTrace, policy *ExecPolicy) {
	if t == nil || policy.active() {
		return
	}
	sink, capture, _ := e.traceSettings()
	if !capture || sink == nil {
		return
	}
	saveCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		saveCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}
	if err := sink.SaveTrace(saveCtx, *t); err != nil {
		e.logger.Warn("capturing turn trace failed",
			"conversation", t.ConversationID, "error", err)
	}
}

// traceHistory converts the assembled wire messages into the payload's history
// window. The leading system message is the built prompt and is stored in its
// own field, so it is skipped here; everything after it is what the model read
// as context, the truncation notice included.
//
// The turn's own message is the last entry either way — a live turn stores it
// before history is loaded, a policy turn has it appended — and it is dropped
// here so the inspector's history block is preceding context only and does not
// repeat the prompt block above it.
func traceHistory(msgs []llm.Message, prompt string) []TraceMessage {
	if len(msgs) <= 1 {
		return nil
	}
	rest := msgs[1:]
	if last := rest[len(rest)-1]; last.Role == "user" && last.Content == prompt {
		rest = rest[:len(rest)-1]
	}
	if len(rest) == 0 {
		return nil
	}
	out := make([]TraceMessage, 0, len(rest))
	for _, m := range rest {
		out = append(out, TraceMessage{Role: m.Role, Content: m.Content})
	}
	return out
}
