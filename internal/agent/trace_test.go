package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
)

// recordingSink captures what the engine hands the trace sink.
type recordingSink struct {
	mu     sync.Mutex
	traces []TurnTrace
	err    error
}

func (s *recordingSink) SaveTrace(_ context.Context, t TurnTrace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces = append(s.traces, t)
	return s.err
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.traces)
}

func (s *recordingSink) last() TurnTrace {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.traces[len(s.traces)-1]
}

func liveTurn(t *testing.T, e *Engine, text string) {
	t.Helper()
	if _, err := e.Chat(context.Background(), adapter.IncomingMessage{
		Adapter: "test", ExternalID: "u1", Text: text,
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

// The whole point of the default: a trace holds everything the model saw, so
// an operator who has not opted in must get nothing recorded even with a sink
// wired and ready.
func TestTraceCapture_OffByDefault(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "hello", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	sink := &recordingSink{}
	e.SetTraceSink(sink)

	if e.TraceCaptureEnabled() {
		t.Fatal("capture reports enabled with no SetTraceCapture call — the default must be off")
	}
	liveTurn(t, e, "hi")

	if got := sink.count(); got != 0 {
		t.Fatalf("captured %d traces with capture off, want 0", got)
	}
}

// Capture on but nothing wired to write to is still no capture: the switch and
// the storage are separate concerns.
func TestTraceCapture_NoSinkRecordsNothing(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "hello", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	e.SetTraceCapture(true, 0)

	if e.TraceCaptureEnabled() {
		t.Fatal("capture reports enabled with no sink wired")
	}
	liveTurn(t, e, "hi")
}

func TestTraceCapture_LiveTurnRecordsPromptHistoryAndToolPayloads(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "read_thing", `{"value":"y"}`),
		{Content: "all done", FinishReason: "stop", Model: "test-model",
			TokensUsed: llm.TokenUsage{Prompt: 11, Completion: 7, Total: 18}},
	}, "autonomous")
	sink := &recordingSink{}
	e.SetTraceSink(sink)
	e.SetTraceCapture(true, 0)

	liveTurn(t, e, "please read y")

	if got := sink.count(); got != 1 {
		t.Fatalf("captured %d traces, want 1", got)
	}
	tr := sink.last()

	if tr.Source != TraceSourceLive {
		t.Errorf("source = %q, want %q", tr.Source, TraceSourceLive)
	}
	if tr.Agent != "pamela" {
		t.Errorf("agent = %q, want pamela", tr.Agent)
	}
	if tr.ConversationID == "" {
		t.Error("conversation id is empty")
	}
	if !strings.Contains(tr.Payload.SystemPrompt, "Current Date") {
		t.Errorf("system prompt does not look like the built prompt: %.80q", tr.Payload.SystemPrompt)
	}
	if tr.Payload.Prompt != "please read y" {
		t.Errorf("prompt = %q", tr.Payload.Prompt)
	}
	if tr.Payload.Response != "all done" {
		t.Errorf("response = %q", tr.Payload.Response)
	}
	// A live turn reaches the model through the store-then-read-back round
	// trip, so its own message is the last history entry.
	if len(tr.Payload.History) == 0 {
		t.Fatal("history window is empty")
	}
	last := tr.Payload.History[len(tr.Payload.History)-1]
	if last.Role != "user" || last.Content != "please read y" {
		t.Errorf("last history message = %+v, want the user's turn", last)
	}
	if tr.Rounds != 1 {
		t.Errorf("rounds = %d, want 1", tr.Rounds)
	}
	if len(tr.Payload.Rounds) != 1 || len(tr.Payload.Rounds[0].ToolCalls) != 1 {
		t.Fatalf("payload rounds = %+v, want one round with one call", tr.Payload.Rounds)
	}
	call := tr.Payload.Rounds[0].ToolCalls[0]
	if call.Tool != "read_thing" {
		t.Errorf("tool = %q, want read_thing", call.Tool)
	}
	if !strings.Contains(call.Arguments, `"value":"y"`) {
		t.Errorf("arguments not captured: %q", call.Arguments)
	}
	if !strings.Contains(call.Result, "read y") {
		t.Errorf("result not captured: %q", call.Result)
	}
	if tr.Tokens.Total != 18 {
		t.Errorf("tokens total = %d, want 18", tr.Tokens.Total)
	}
	if tr.Bytes <= 0 {
		t.Errorf("bytes = %d, want the encoded payload size", tr.Bytes)
	}
	if tr.Truncated {
		t.Error("small trace reported as truncated")
	}
}

// A sink failure is bookkeeping, not the turn: the user still gets an answer.
func TestTraceCapture_SinkFailureDoesNotFailTheTurn(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "hello", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	sink := &recordingSink{err: context.DeadlineExceeded}
	e.SetTraceSink(sink)
	e.SetTraceCapture(true, 0)

	resp, err := e.Chat(context.Background(), adapter.IncomingMessage{Adapter: "test", ExternalID: "u1", Text: "hi"})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp != "hello" {
		t.Errorf("response = %q, want hello", resp)
	}
}

// An eval sample always carries its trace — the judge reads it — and the engine
// still writes nothing, because a policy turn's isolation is structural.
func TestTraceCapture_PolicyTurnAlwaysTracesAndNeverPersists(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "write_thing", `{"value":"x"}`),
		{Content: "done", FinishReason: "stop", Model: "test-model"},
	}, "autonomous")
	sink := &recordingSink{}
	e.SetTraceSink(sink)
	// Capture deliberately left off: an eval sample does not depend on it.

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, ExecPolicy{
		Kind: ExecEval, Variant: "candidate", Model: "candidate-model", ConvID: "eval:1:2:0:3",
	})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.Trace == nil {
		t.Fatal("policy turn returned no trace")
	}
	if got := sink.count(); got != 0 {
		t.Fatalf("policy turn wrote %d traces to the sink, want 0", got)
	}
	if result.Trace.Source != TraceSourceEval {
		t.Errorf("source = %q, want %q", result.Trace.Source, TraceSourceEval)
	}
	if result.Trace.RequestedModel != "candidate-model" {
		t.Errorf("requested model = %q, want candidate-model", result.Trace.RequestedModel)
	}
	if result.Trace.ConversationID != "eval:1:2:0:3" {
		t.Errorf("conversation id = %q", result.Trace.ConversationID)
	}
	// The suppressed write is part of what happened and must be in the trace.
	if len(result.Trace.Payload.Rounds) != 1 {
		t.Fatalf("rounds = %+v, want one", result.Trace.Payload.Rounds)
	}
	if got := result.Trace.Payload.Rounds[0].ToolCalls[0].Outcome; got != outcomeSuppressed {
		t.Errorf("outcome = %q, want %q", got, outcomeSuppressed)
	}
}

func TestTruncateTracePayload_DropsOldestRoundsFirst(t *testing.T) {
	big := strings.Repeat("x", 2000)
	p := TracePayload{
		SystemPrompt: "prompt",
		History:      []TraceMessage{{Role: "user", Content: "earlier"}},
		Prompt:       "now",
		Response:     "answer",
	}
	for i := 1; i <= 5; i++ {
		p.Rounds = append(p.Rounds, TraceRound{
			Round:     i,
			ToolCalls: []TraceToolCall{{Tool: "t", Result: big}},
		})
	}

	out, truncated := truncateTracePayload(p, 6*1024)
	if !truncated {
		t.Fatal("payload over the cap reported as untruncated")
	}
	if payloadSize(out) > 6*1024 {
		t.Fatalf("payload still %d bytes, over the 6144 cap", payloadSize(out))
	}
	if len(out.Rounds) == 0 {
		t.Fatal("every round dropped; the cap should have been met before that")
	}
	// Oldest first: the surviving rounds must be the tail.
	if out.Rounds[len(out.Rounds)-1].Round != 5 {
		t.Errorf("last surviving round = %d, want 5 — the newest round must survive", out.Rounds[len(out.Rounds)-1].Round)
	}
	if out.Rounds[0].Round == 1 {
		t.Error("round 1 survived; truncation must drop the oldest first")
	}
	if out.Truncation == nil || out.Truncation.DroppedRounds == 0 {
		t.Fatalf("truncation not recorded: %+v", out.Truncation)
	}
	if out.Truncation.DroppedHistory != 0 {
		t.Errorf("history dropped (%d) while rounds were still available", out.Truncation.DroppedHistory)
	}
	if !strings.Contains(out.Truncation.Note, "dropped") {
		t.Errorf("note = %q", out.Truncation.Note)
	}
	if out.Response != "answer" {
		t.Error("response text lost to round truncation")
	}
}

func TestTruncateTracePayload_DropsHistoryOnceRoundsAreGone(t *testing.T) {
	big := strings.Repeat("y", 1500)
	p := TracePayload{
		SystemPrompt: "sys",
		Prompt:       "now",
		Response:     "answer",
	}
	for i := 0; i < 6; i++ {
		p.History = append(p.History, TraceMessage{Role: "user", Content: big})
	}
	p.Rounds = []TraceRound{{Round: 1, ToolCalls: []TraceToolCall{{Tool: "t", Result: big}}}}

	out, truncated := truncateTracePayload(p, 4*1024)
	if !truncated {
		t.Fatal("payload over the cap reported as untruncated")
	}
	if out.Truncation.DroppedRounds != 1 {
		t.Errorf("dropped rounds = %d, want 1 (rounds go first)", out.Truncation.DroppedRounds)
	}
	if out.Truncation.DroppedHistory == 0 {
		t.Error("history untouched even though the cap was still exceeded")
	}
	if len(out.History) > 0 && out.History[len(out.History)-1].Content != big {
		t.Error("the newest history message must survive")
	}
	if payloadSize(out) > 4*1024 {
		t.Fatalf("payload still %d bytes, over the 4096 cap", payloadSize(out))
	}
}

func TestTruncateTracePayload_UnderCapIsUntouched(t *testing.T) {
	p := TracePayload{SystemPrompt: "sys", Prompt: "p", Response: "r",
		Rounds: []TraceRound{{Round: 1, ToolCalls: []TraceToolCall{{Tool: "t"}}}}}
	out, truncated := truncateTracePayload(p, DefaultMaxTraceBytes)
	if truncated {
		t.Fatal("small payload reported as truncated")
	}
	if out.Truncation != nil {
		t.Errorf("truncation recorded on an untouched payload: %+v", out.Truncation)
	}
	if len(out.Rounds) != 1 {
		t.Errorf("rounds changed: %+v", out.Rounds)
	}
}

// A zero cap must not read as "unbounded": one pathological turn would fill
// the table.
func TestTruncateTracePayload_ZeroCapUsesTheDefault(t *testing.T) {
	p := TracePayload{Response: strings.Repeat("z", DefaultMaxTraceBytes+1000)}
	out, truncated := truncateTracePayload(p, 0)
	if !truncated {
		t.Fatal("payload over the default cap reported as untruncated")
	}
	if payloadSize(out) > DefaultMaxTraceBytes {
		t.Fatalf("payload = %d bytes, over the default cap", payloadSize(out))
	}
}

func TestTracePayload_RoundTripsThroughJSON(t *testing.T) {
	tr := TurnTrace{Payload: TracePayload{
		SystemPrompt: "sys",
		History:      []TraceMessage{{Role: "user", Content: "hi"}},
		Prompt:       "hi",
		Response:     "there",
		Rounds:       []TraceRound{{Round: 1, ToolCalls: []TraceToolCall{{Tool: "kv_get", Arguments: `{"k":"v"}`, Result: "ok"}}}},
	}}
	raw, err := tr.EncodePayload()
	if err != nil {
		t.Fatalf("EncodePayload: %v", err)
	}
	back, err := DecodeTracePayload(raw)
	if err != nil {
		t.Fatalf("DecodeTracePayload: %v", err)
	}
	if back.Rounds[0].ToolCalls[0].Arguments != `{"k":"v"}` {
		t.Errorf("arguments lost: %+v", back.Rounds[0].ToolCalls[0])
	}
	if back.SystemPrompt != "sys" || back.Response != "there" {
		t.Errorf("payload changed across the round trip: %+v", back)
	}
}

func TestDecodeTracePayload_EmptyIsZeroValue(t *testing.T) {
	p, err := DecodeTracePayload("")
	if err != nil {
		t.Fatalf("DecodeTracePayload: %v", err)
	}
	if p.SystemPrompt != "" || len(p.Rounds) != 0 {
		t.Errorf("empty blob decoded to %+v", p)
	}
}

func TestGroupTraceRounds_PreservesOrderWithinAndAcrossRounds(t *testing.T) {
	rounds := groupTraceRounds([]ToolCallRecord{
		{ToolName: "a", Round: 1},
		{ToolName: "b", Round: 1},
		{ToolName: "c", Round: 2},
	})
	if len(rounds) != 2 {
		t.Fatalf("rounds = %d, want 2", len(rounds))
	}
	if rounds[0].Round != 1 || len(rounds[0].ToolCalls) != 2 {
		t.Fatalf("first round = %+v", rounds[0])
	}
	if rounds[0].ToolCalls[0].Tool != "a" || rounds[0].ToolCalls[1].Tool != "b" {
		t.Errorf("call order changed: %+v", rounds[0].ToolCalls)
	}
	if rounds[1].Round != 2 || rounds[1].ToolCalls[0].Tool != "c" {
		t.Errorf("second round = %+v", rounds[1])
	}
}

// The trace is the one place arguments and results are written down: they are
// db:"-" on ToolCallRecord and reach the database nowhere else.
func TestTurnTrace_CarriesPayloadsRecordsDrop(t *testing.T) {
	tr := TurnTrace{Payload: TracePayload{Rounds: groupTraceRounds([]ToolCallRecord{
		{ToolName: "kv_set", Round: 1, Arguments: `{"key":"secret"}`, Result: "stored"},
	})}}
	raw, err := json.Marshal(tr.Payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), "secret") || !strings.Contains(string(raw), "stored") {
		t.Errorf("payload dropped the tool arguments or result: %s", raw)
	}
}
