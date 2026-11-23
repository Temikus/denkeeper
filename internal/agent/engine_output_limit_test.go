package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

const outputLimitMarker = "[engine: turn ended early — the model hit its output-token limit]"

// cutOffCall is a response the provider ended on the output limit in the middle
// of a read_thing call, so its arguments are not valid JSON.
func cutOffCall() *llm.ChatResponse {
	resp := toolCallResponse("cut", "read_thing", `{"value":"ab`)
	resp.FinishReason = "length"
	return resp
}

func finalText(text string) *llm.ChatResponse {
	return &llm.ChatResponse{Content: text, FinishReason: "stop", Model: "test-model"}
}

func assertNoFaultRecords(t *testing.T, records []ToolCallRecord) {
	t.Helper()
	for _, r := range records {
		if r.Outcome == "failed" || r.Outcome == "rejected" {
			t.Errorf("record %s has outcome %q; a cut-off call must not read as a fault", r.ToolName, r.Outcome)
		}
	}
}

func TestOutputLimit_FirstResponseCutOffCallDoesNotRun(t *testing.T) {
	e, _, reads, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		cutOffCall(),
		finalText("I ran out of room before I could look that up."),
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if got := reads.Load(); got != 0 {
		t.Errorf("read_thing ran %d times, want 0", got)
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("recorded %d tool calls, want none", len(result.ToolCalls))
	}
	if result.StopReason != "output_limit" {
		t.Errorf("StopReason = %q, want output_limit", result.StopReason)
	}
	if !strings.Contains(result.Response, outputLimitMarker) {
		t.Errorf("Response = %q, want the turn-ended-early marker", result.Response)
	}
	if !strings.HasPrefix(result.Response, "I ran out of room") {
		t.Errorf("Response = %q, want the wrap-up text first", result.Response)
	}
}

func TestOutputLimit_AfterExecutedRoundKeepsItsRecord(t *testing.T) {
	e, _, reads, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "read_thing", `{"value":"x"}`),
		cutOffCall(),
		finalText("Read x; the second lookup was cut off."),
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("read_thing ran %d times, want 1 (only the complete call)", got)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Outcome != "ok" {
		t.Fatalf("tool calls = %+v, want the one ok record", result.ToolCalls)
	}
	assertNoFaultRecords(t, result.ToolCalls)
	if result.StopReason != "output_limit" {
		t.Errorf("StopReason = %q, want output_limit", result.StopReason)
	}
	if !strings.Contains(result.Response, outputLimitMarker) {
		t.Errorf("Response = %q, want the marker", result.Response)
	}
}

func TestOutputLimit_TextWithoutCallIsTheReply(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "A long answer that got cut", FinishReason: "length", Model: "test-model"},
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.Response != "A long answer that got cut" {
		t.Errorf("Response = %q, want the text as is", result.Response)
	}
	if result.StopReason != "" {
		t.Errorf("StopReason = %q, want none", result.StopReason)
	}
}

func TestOutputLimit_NoTextNoCallSetsStopReason(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "  ", FinishReason: "length", Model: "test-model"},
		finalText("Short answer."),
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.StopReason != "output_limit" {
		t.Errorf("StopReason = %q, want output_limit", result.StopReason)
	}
	if !strings.Contains(result.Response, outputLimitMarker) {
		t.Errorf("Response = %q, want the marker", result.Response)
	}
}

// With nothing to summarise and an empty wrap-up, the turn still ends with the
// marker rather than an error.
func TestOutputLimit_EmptyWrapUpFallsBackToMarker(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		cutOffCall(),
		finalText(""),
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if result.Response != outputLimitMarker {
		t.Errorf("Response = %q, want only the marker", result.Response)
	}
	if result.StopReason != "output_limit" {
		t.Errorf("StopReason = %q, want output_limit", result.StopReason)
	}
}

func TestOutputLimit_OrdinaryToolRoundUnchanged(t *testing.T) {
	e, _, reads, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "read_thing", `{"value":"x"}`),
		finalText("done"),
	}, "autonomous")

	result, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy())
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if reads.Load() != 1 || result.Response != "done" || result.StopReason != "" {
		t.Errorf("reads=%d response=%q stop=%q; want 1, done, none", reads.Load(), result.Response, result.StopReason)
	}
}

// cutOffStepA is a stop-test response ended on the output limit in the middle
// of a step_a call.
func cutOffStepA() *llm.ChatResponse {
	return &llm.ChatResponse{
		ToolCalls:    []llm.ToolCall{{ID: "cut", Type: "function", Function: llm.FunctionCall{Name: "step_a", Arguments: `{"query":"x`}}},
		Model:        "test-model",
		FinishReason: "length",
	}
}

const stopRequestedMarker = "[engine: turn ended early — stop requested]"

// A panic raised while the cut-off completion is in flight must not spend a
// wrap-up: the emergency stop wins over the output limit.
func TestOutputLimit_PanicDuringInitialCompletionSpendsNothing(t *testing.T) {
	var engine *Engine
	provider := &hookedSequentialProvider{responses: []*llm.ChatResponse{cutOffStepA()}}
	provider.beforeCall = func(call int) {
		if call == 1 {
			engine.RequestStop()
		}
	}
	engine, _, execCount := newStopTestEngine(t, provider, nil)

	result, err := engine.ChatWithEvents(context.Background(), stopTestMessage("limit-panic-initial"), nil)
	if err != nil {
		t.Fatalf("stopped turn should end gracefully, got error: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Errorf("got %d provider requests, want 1 (no wrap-up)", len(provider.requests))
	}
	if !strings.Contains(result, stopRequestedMarker) {
		t.Errorf("result = %q, want the stop-requested marker", result)
	}
	if execCount.Load() != 0 {
		t.Errorf("executed %d calls, want 0", execCount.Load())
	}
}

func TestOutputLimit_PanicDuringFollowUpSpendsNothing(t *testing.T) {
	var engine *Engine
	provider := &hookedSequentialProvider{responses: []*llm.ChatResponse{
		toolRoundResponse("", stopToolCall("c1", "step_a")),
		cutOffStepA(),
	}}
	provider.beforeCall = func(call int) {
		if call == 2 {
			engine.RequestStop()
		}
	}
	engine, _, execCount := newStopTestEngine(t, provider, nil)

	result, err := engine.ChatWithEvents(context.Background(), stopTestMessage("limit-panic-followup"), nil)
	if err != nil {
		t.Fatalf("stopped turn should end gracefully, got error: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Errorf("got %d provider requests, want 2 (no wrap-up)", len(provider.requests))
	}
	if !strings.Contains(result, stopRequestedMarker) {
		t.Errorf("result = %q, want the stop-requested marker", result)
	}
	if execCount.Load() != 1 {
		t.Errorf("executed %d calls, want 1 (the complete round only)", execCount.Load())
	}
}

// A cancel during the cut-off completion is a cancel, not an output-limit stop.
func TestOutputLimit_CancelDuringInitialCompletionIsACancel(t *testing.T) {
	var engine *Engine
	const sessionID = "limit-cancel-initial"
	provider := &hookedSequentialProvider{responses: []*llm.ChatResponse{cutOffStepA()}}
	provider.beforeCall = func(call int) {
		if call == 1 && !engine.RequestStopChat("telegram", sessionID) {
			t.Error("RequestStopChat did not reach the in-flight turn")
		}
	}
	engine, _, _ = newStopTestEngine(t, provider, nil)

	result, err := engine.ChatWithEvents(context.Background(), stopTestMessage(sessionID), nil)
	if err != nil {
		t.Fatalf("cancelled turn should end gracefully, got error: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Errorf("got %d provider requests, want 1", len(provider.requests))
	}
	if !strings.Contains(result, "[engine: turn ended early — cancelled at your request]") {
		t.Errorf("result = %q, want the cancel marker", result)
	}
}

func TestOutputLimit_WrapUpCarriesItsOwnInstruction(t *testing.T) {
	provider := &sequentialProvider{responses: []*llm.ChatResponse{cutOffCall(), finalText("Nothing ran.")}}
	e, _, _, _ := newPolicyTestEngineWithProvider(t, provider, "autonomous")

	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy()); err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(provider.requests))
	}
	msgs := provider.requests[1].Messages
	last := msgs[len(msgs)-1].Content
	want := "[engine: your last response hit the output-token limit and was cut off, so nothing in it ran or reached the user. " +
		"Tools are no longer available this turn. Briefly tell the user what was done and what was not, " +
		"using only the tool results above if there are any. Do not guess the result of a call that did not run.]"
	if last != want {
		t.Errorf("wrap-up instruction = %q, want %q", last, want)
	}
}

// llmEventDetails decodes the detail of every llm audit event, in order.
func llmEventDetails(t *testing.T, events []audit.Event) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range events {
		if ev.Category != audit.CategoryLLM {
			continue
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(ev.Detail), &d); err != nil {
			t.Fatalf("decoding detail %q: %v", ev.Detail, err)
		}
		out = append(out, d)
	}
	return out
}

func TestOutputLimit_AuditFlagsTheLengthStop(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		toolCallResponse("c1", "read_thing", `{"value":"x"}`),
		cutOffCall(),
		finalText("summary"),
	}, "autonomous")
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy()); err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	details := llmEventDetails(t, auditor.events)
	if len(details) != 3 {
		t.Fatalf("got %d llm events, want 3", len(details))
	}
	if details[1]["output_limit"] != true || details[1]["pending_tool_calls"] != float64(1) {
		t.Errorf("length-stop event = %v, want output_limit and pending_tool_calls 1", details[1])
	}
	for _, i := range []int{0, 2} {
		if _, ok := details[i]["output_limit"]; ok {
			t.Errorf("event %d carries output_limit: %v", i, details[i])
		}
		if _, ok := details[i]["pending_tool_calls"]; ok {
			t.Errorf("event %d carries pending_tool_calls: %v", i, details[i])
		}
	}
}

func TestOutputLimit_AuditFlagWithoutCallsOmitsPending(t *testing.T) {
	e, _, _, _ := newPolicyTestEngine(t, []*llm.ChatResponse{
		{Content: "cut", FinishReason: "length", Model: "test-model"},
	}, "autonomous")
	auditor := &collectingAuditor{}
	e.SetAuditor(auditor)

	if _, err := e.DryRun(context.Background(), adapter.IncomingMessage{Text: "go"}, dryRunPolicy()); err != nil {
		t.Fatalf("DryRun: %v", err)
	}
	details := llmEventDetails(t, auditor.events)
	if len(details) != 1 || details[0]["output_limit"] != true {
		t.Fatalf("events = %v, want one with output_limit", details)
	}
	if _, ok := details[0]["pending_tool_calls"]; ok {
		t.Errorf("pending_tool_calls present with no calls: %v", details[0])
	}
}
