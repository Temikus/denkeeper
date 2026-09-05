package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
)

// defaultTestReplyGuard mirrors what applyReplyGuardDefaults produces, so the
// tests exercise the shipped policy rather than an invented one.
func defaultTestReplyGuard() ReplyGuard {
	return ReplyGuard{
		Enabled:          true,
		OnRoleMarkup:     GuardWithhold,
		OnOversized:      GuardWithhold,
		OnNoToolCalls:    GuardWarn,
		OnLeakedToolCall: GuardWithhold,
		MaxReplyBytes:    defaultMaxReplyBytes,
		ExcerptBytes:     defaultExcerptBytes,
	}
}

// newGuardTestEngine builds a toolless engine whose provider always returns
// content, plus the store, sent-message sink and auditor to assert against.
func newGuardTestEngine(t *testing.T, content string, completionTokens int, guard ReplyGuard) (*Engine, *SQLiteMemoryStore, *sentMessages, *collectingAuditor) {
	t.Helper()

	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	router := llm.NewRouter("mock", "test-model", llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil))
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      content,
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: completionTokens, Total: 20 + completionTokens},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	sent := &sentMessages{}
	auditor := &collectingAuditor{}
	e := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())
	e.SetAuditor(auditor)
	e.SetReplyGuard(guard)
	return e, store, sent, auditor
}

// scheduledMsg is the synthetic message the scheduler dispatches.
func scheduledMsg(skillName string) adapter.IncomingMessage {
	return adapter.IncomingMessage{
		Adapter:      "test",
		ExternalID:   "chat-1",
		UserName:     "scheduler",
		Text:         "[Scheduled: curiosity-loop | 2026-08-18T09:00:00+10:00 | 2026-W34]",
		SkillName:    skillName,
		ScheduleName: "curiosity-loop",
		IsScheduled:  true,
		Timestamp:    time.Now(),
	}
}

// guardEvents returns only the reply-guard audit events.
func guardEvents(events []audit.Event) []audit.Event {
	var out []audit.Event
	for _, ev := range events {
		if ev.Category == audit.CategorySafety && ev.Action == "reply_guard" {
			out = append(out, ev)
		}
	}
	return out
}

// guardDetail decodes one reply-guard event's detail payload.
func guardDetail(t *testing.T, ev audit.Event) map[string]any {
	t.Helper()
	var detail map[string]any
	if err := json.Unmarshal([]byte(ev.Detail), &detail); err != nil {
		t.Fatalf("decoding audit detail: %v", err)
	}
	return detail
}

// storedAssistantContent returns the assistant message persisted for convID.
func storedAssistantContent(t *testing.T, store *SQLiteMemoryStore, convID string) string {
	t.Helper()
	msgs, err := store.GetMessages(context.Background(), convID, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			return msgs[i].Content
		}
	}
	t.Fatalf("no assistant message stored for %q", convID)
	return ""
}

const guardTestConvID = "default:test:chat-1"

func TestReplyGuard_ScheduledOversizedReplyWithheld(t *testing.T) {
	raw := strings.Repeat("confabulation. ", 4000) // ~60 KB, no tool calls
	e, store, sent, auditor := newGuardTestEngine(t, raw, 17000, defaultTestReplyGuard())

	if err := e.HandleMessage(context.Background(), scheduledMsg("curiosity-loop")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
	wire := sent.msgs[0].Text
	if !strings.HasPrefix(wire, "[engine: reply withheld — "+signalOversized) {
		t.Errorf("wire text = %q, want the withheld notice naming %s", wire, signalOversized)
	}
	if strings.Contains(wire, "confabulation") {
		t.Error("wire text leaked the raw reply")
	}

	if got := storedAssistantContent(t, store, guardTestConvID); got != raw {
		t.Errorf("stored content len = %d, want the raw reply (%d bytes)", len(got), len(raw))
	}

	events := guardEvents(auditor.events)
	if len(events) != 1 {
		t.Fatalf("emitted %d reply_guard events, want 1", len(events))
	}
	if events[0].Status != audit.StatusDenied {
		t.Errorf("status = %q, want %q", events[0].Status, audit.StatusDenied)
	}
	if events[0].ConversationID != guardTestConvID {
		t.Errorf("conversation = %q, want %q", events[0].ConversationID, guardTestConvID)
	}

	detail := guardDetail(t, events[0])
	if detail["action"] != GuardWithhold {
		t.Errorf("detail action = %v, want %q", detail["action"], GuardWithhold)
	}
	if got := detail["reply_bytes"]; got != float64(len(raw)) {
		t.Errorf("detail reply_bytes = %v, want %d", got, len(raw))
	}
	if got := detail["tool_calls"]; got != float64(0) {
		t.Errorf("detail tool_calls = %v, want 0", got)
	}
	if excerpt, _ := detail["excerpt"].(string); len(excerpt) > defaultExcerptBytes {
		t.Errorf("excerpt is %d bytes, want at most %d", len(excerpt), defaultExcerptBytes)
	}
}

func TestReplyGuard_ScheduledRoleMarkupWithheld(t *testing.T) {
	raw := `<rs_tool_calls><rs_tool name="tool_list">{}</rs_tool></rs_tool_calls>`
	e, store, sent, auditor := newGuardTestEngine(t, raw, 40, defaultTestReplyGuard())

	if err := e.HandleMessage(context.Background(), scheduledMsg("curiosity-loop")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
	if !strings.HasPrefix(sent.msgs[0].Text, "[engine: reply withheld — "+signalRoleMarkup) {
		t.Errorf("wire text = %q, want the withheld notice naming %s", sent.msgs[0].Text, signalRoleMarkup)
	}
	if strings.Contains(sent.msgs[0].Text, "rs_tool") {
		t.Error("wire text leaked the raw markup")
	}

	if got := storedAssistantContent(t, store, guardTestConvID); got != raw {
		t.Errorf("stored content = %q, want the raw reply", got)
	}

	events := guardEvents(auditor.events)
	if len(events) != 1 {
		t.Fatalf("emitted %d reply_guard events, want 1", len(events))
	}
	// The turn also made no tool calls, which is its own (warn-level) signal;
	// the withholding one leads and decides the action.
	detail := guardDetail(t, events[0])
	signals, _ := detail["signals"].([]any)
	if len(signals) != 2 || signals[0] != signalRoleMarkup || signals[1] != signalNoToolCalls {
		t.Errorf("detail signals = %v, want [%s %s]", signals, signalRoleMarkup, signalNoToolCalls)
	}
	if detail["action"] != GuardWithhold {
		t.Errorf("detail action = %v, want %q — withhold must beat warn", detail["action"], GuardWithhold)
	}
}

func TestReplyGuard_ScheduledNormalReplyDelivered(t *testing.T) {
	raw := "Checked the feeds, nothing new worth flagging today."
	// SkillName empty: an ordinary scheduled nudge with no skill to blame for
	// the absent tool calls.
	e, _, sent, auditor := newGuardTestEngine(t, raw, 60, defaultTestReplyGuard())

	if err := e.HandleMessage(context.Background(), scheduledMsg("")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(sent.msgs) != 1 || sent.msgs[0].Text != raw {
		t.Errorf("wire text = %q, want the reply unchanged", sent.msgs[0].Text)
	}
	if events := guardEvents(auditor.events); len(events) != 0 {
		t.Errorf("emitted %d reply_guard events, want 0", len(events))
	}
}

func TestReplyGuard_LiveTurnUnaffected(t *testing.T) {
	cases := map[string]string{
		"oversized": strings.Repeat("confabulation. ", 4000),
		"markup":    `<rs_tool_calls><rs_tool name="tool_list">{}</rs_tool></rs_tool_calls>`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			e, store, sent, auditor := newGuardTestEngine(t, raw, 17000, defaultTestReplyGuard())

			msg := scheduledMsg("curiosity-loop")
			msg.IsScheduled = false
			msg.UserName = "testuser"
			if err := e.HandleMessage(context.Background(), msg); err != nil {
				t.Fatalf("HandleMessage: %v", err)
			}

			if len(sent.msgs) != 1 || sent.msgs[0].Text != raw {
				t.Errorf("live turn wire text was altered (len %d, want %d)", len(sent.msgs[0].Text), len(raw))
			}
			if got := storedAssistantContent(t, store, guardTestConvID); got != raw {
				t.Error("live turn stored content was altered")
			}
			if events := guardEvents(auditor.events); len(events) != 0 {
				t.Errorf("emitted %d reply_guard events on a live turn, want 0", len(events))
			}
		})
	}
}

func TestReplyGuard_NoToolCallsWarnsButDelivers(t *testing.T) {
	raw := "I'm ready to help! What would you like me to do?"
	e, store, sent, auditor := newGuardTestEngine(t, raw, 20, defaultTestReplyGuard())

	if err := e.HandleMessage(context.Background(), scheduledMsg("curiosity-loop")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(sent.msgs) != 1 || sent.msgs[0].Text != raw {
		t.Errorf("wire text = %q, want the reply delivered unchanged under %q", sent.msgs[0].Text, GuardWarn)
	}
	if got := storedAssistantContent(t, store, guardTestConvID); got != raw {
		t.Errorf("stored content = %q, want the raw reply", got)
	}

	events := guardEvents(auditor.events)
	if len(events) != 1 {
		t.Fatalf("emitted %d reply_guard events, want 1", len(events))
	}
	if events[0].Status != audit.StatusError {
		t.Errorf("status = %q, want %q", events[0].Status, audit.StatusError)
	}
	detail := guardDetail(t, events[0])
	if detail["action"] != GuardWarn {
		t.Errorf("detail action = %v, want %q", detail["action"], GuardWarn)
	}
	signals, _ := detail["signals"].([]any)
	if len(signals) != 1 || signals[0] != signalNoToolCalls {
		t.Errorf("detail signals = %v, want [%s]", signals, signalNoToolCalls)
	}
	if detail["skill"] != "curiosity-loop" || detail["schedule"] != "curiosity-loop" {
		t.Errorf("detail lost the schedule attribution: %v", detail)
	}
}

func TestReplyGuard_MultipleSignalsRecordAll(t *testing.T) {
	// Oversized *and* carrying markup: the notice names the markup, which is
	// the more diagnostic of the two.
	raw := `<rs_tool_calls>` + strings.Repeat("x", defaultMaxReplyBytes+1)
	e, _, sent, auditor := newGuardTestEngine(t, raw, 9000, defaultTestReplyGuard())

	if err := e.HandleMessage(context.Background(), scheduledMsg("curiosity-loop")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if !strings.HasPrefix(sent.msgs[0].Text, "[engine: reply withheld — "+signalRoleMarkup) {
		t.Errorf("wire text = %q, want the notice to name %s", sent.msgs[0].Text, signalRoleMarkup)
	}

	events := guardEvents(auditor.events)
	if len(events) != 1 {
		t.Fatalf("emitted %d reply_guard events, want 1", len(events))
	}
	signals, _ := guardDetail(t, events[0])["signals"].([]any)
	if len(signals) != 3 {
		t.Fatalf("detail signals = %v, want all three to be recorded", signals)
	}
	want := []string{signalRoleMarkup, signalOversized, signalNoToolCalls}
	for i, sig := range want {
		if signals[i] != sig {
			t.Errorf("signals[%d] = %v, want %s", i, signals[i], sig)
		}
	}
}

func TestReplyGuard_DisabledPassesThrough(t *testing.T) {
	raw := strings.Repeat("confabulation. ", 4000)
	guard := defaultTestReplyGuard()
	guard.Enabled = false
	e, _, sent, auditor := newGuardTestEngine(t, raw, 17000, guard)

	if err := e.HandleMessage(context.Background(), scheduledMsg("curiosity-loop")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(sent.msgs) != 1 || sent.msgs[0].Text != raw {
		t.Error("disabled guard altered the reply")
	}
	if events := guardEvents(auditor.events); len(events) != 0 {
		t.Errorf("emitted %d reply_guard events with the guard disabled, want 0", len(events))
	}
}

func TestReplyGuard_CompletionTokenCapTripsIndependently(t *testing.T) {
	// Short reply, huge completion count: the byte measure cannot see this.
	raw := "ok"
	guard := defaultTestReplyGuard()
	guard.MaxCompletionTokens = 8000
	e, _, sent, auditor := newGuardTestEngine(t, raw, 17000, guard)

	msg := scheduledMsg("")
	if err := e.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if !strings.HasPrefix(sent.msgs[0].Text, "[engine: reply withheld — "+signalOversizeTokens) {
		t.Errorf("wire text = %q, want the notice naming %s", sent.msgs[0].Text, signalOversizeTokens)
	}
	events := guardEvents(auditor.events)
	if len(events) != 1 {
		t.Fatalf("emitted %d reply_guard events, want 1", len(events))
	}
	if got := guardDetail(t, events[0])["completion_tokens"]; got != float64(17000) {
		t.Errorf("detail completion_tokens = %v, want 17000", got)
	}
}

func TestReplyGuard_DryRunReportsWithoutSubstituting(t *testing.T) {
	raw := `<rs_tool_calls><rs_tool name="tool_list">{}</rs_tool></rs_tool_calls>`
	e, store, sent, auditor := newGuardTestEngine(t, raw, 40, defaultTestReplyGuard())

	result, err := e.DryRun(context.Background(), scheduledMsg("curiosity-loop"), ExecPolicy{
		Kind:   ExecDryRun,
		ConvID: "dryrun:guard",
	})
	if err != nil {
		t.Fatalf("DryRun: %v", err)
	}

	if result.Response != raw {
		t.Errorf("preview response = %q, want the raw reply — a preview must show what the model produced", result.Response)
	}
	if result.ReplyGuard == nil {
		t.Fatal("preview reported no reply-guard verdict")
	}
	if result.ReplyGuard.Action != GuardWithhold {
		t.Errorf("verdict action = %q, want %q", result.ReplyGuard.Action, GuardWithhold)
	}
	if len(result.ReplyGuard.Signals) != 2 || result.ReplyGuard.Signals[0] != signalRoleMarkup {
		t.Errorf("verdict signals = %v, want %s first", result.ReplyGuard.Signals, signalRoleMarkup)
	}
	if !strings.HasPrefix(result.ReplyGuard.Notice, "[engine: reply withheld — ") {
		t.Errorf("verdict notice = %q, want the operator notice a live turn would deliver", result.ReplyGuard.Notice)
	}

	if len(sent.msgs) != 0 {
		t.Errorf("preview sent %d messages, want 0", len(sent.msgs))
	}
	msgs, err := store.GetMessages(context.Background(), "dryrun:guard", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("preview persisted %d messages, want 0", len(msgs))
	}

	events := guardEvents(auditor.events)
	if len(events) != 1 {
		t.Fatalf("emitted %d reply_guard events, want 1", len(events))
	}
	if events[0].Source != string(ExecDryRun) {
		t.Errorf("event source = %q, want %q", events[0].Source, ExecDryRun)
	}
	if !strings.HasSuffix(events[0].Agent, "#dryrun") {
		t.Errorf("event agent = %q, want the dry-run pseudo-identity", events[0].Agent)
	}
}

func TestReplyGuard_OffActionSkipsSignal(t *testing.T) {
	raw := strings.Repeat("confabulation. ", 4000)
	guard := defaultTestReplyGuard()
	guard.OnOversized = GuardOff
	e, _, sent, auditor := newGuardTestEngine(t, raw, 17000, guard)

	if err := e.HandleMessage(context.Background(), scheduledMsg("")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if sent.msgs[0].Text != raw {
		t.Error("oversized signal set to off still altered the reply")
	}
	if events := guardEvents(auditor.events); len(events) != 0 {
		t.Errorf("emitted %d reply_guard events, want 0", len(events))
	}
}

func TestReplyGuard_LeakedToolCallWithheld(t *testing.T) {
	raw := "Let me start with the idempotency guard.functions.kv_get:0{\"key\": \"log:heartbeat:2026-09-05\"}functions.kv_get:1{\"key\": \"log:heartbeat:2026-09-04\"}"
	e, store, sent, auditor := newGuardTestEngine(t, raw, 120, defaultTestReplyGuard())

	if err := e.HandleMessage(context.Background(), scheduledMsg("heartbeat")); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
	if !strings.HasPrefix(sent.msgs[0].Text, "[engine: reply withheld — "+signalLeakedToolCall) {
		t.Errorf("wire text = %q, want the withheld notice naming %s", sent.msgs[0].Text, signalLeakedToolCall)
	}
	if strings.Contains(sent.msgs[0].Text, "functions.kv_get") {
		t.Error("wire text leaked the raw call")
	}
	if got := storedAssistantContent(t, store, guardTestConvID); got != raw {
		t.Errorf("stored content = %q, want the raw reply", got)
	}

	events := guardEvents(auditor.events)
	if len(events) != 1 {
		t.Fatalf("emitted %d reply_guard events, want 1", len(events))
	}
	detail := guardDetail(t, events[0])
	signals, _ := detail["signals"].([]any)
	if len(signals) != 2 || signals[0] != signalLeakedToolCall || signals[1] != signalNoToolCalls {
		t.Errorf("detail signals = %v, want [%s %s]", signals, signalLeakedToolCall, signalNoToolCalls)
	}
	if detail["action"] != GuardWithhold {
		t.Errorf("detail action = %v, want %q", detail["action"], GuardWithhold)
	}
}

// The leak can happen on any round, so unlike no_tool_calls the signal must
// fire even when tools ran earlier in the turn.
func TestReplyGuard_LeakedToolCallTripsAfterToolRounds(t *testing.T) {
	g := defaultTestReplyGuard()
	content := "Wait, I called it twice.   functions.find-tasks-by-date:8{\"startDate\": \"today\"}"
	records := []ToolCallRecord{{ToolName: "find-tasks", Outcome: "ok"}, {ToolName: "kv_get", Outcome: "ok"}}
	res := evaluateReplyGuard(g, scheduledMsg("heartbeat"), content, &llm.ChatResponse{FinishReason: "stop"}, records)
	if !res.tripped() || res.Action != GuardWithhold {
		t.Fatalf("expected a withholding trip, got signals=%v action=%q", res.Signals, res.Action)
	}
	if len(res.Signals) != 1 || res.Signals[0] != signalLeakedToolCall {
		t.Errorf("signals = %v, want only %s (tools did run)", res.Signals, signalLeakedToolCall)
	}
}

func TestReplyGuard_LeakedToolCallOffSkipsSignal(t *testing.T) {
	g := defaultTestReplyGuard()
	g.OnLeakedToolCall = GuardOff
	content := "functions.kv_get:0{\"key\": \"a\"}"
	res := evaluateReplyGuard(g, scheduledMsg("heartbeat"), content, &llm.ChatResponse{FinishReason: "stop"}, []ToolCallRecord{{ToolName: "kv_get"}})
	if res.tripped() {
		t.Errorf("signal off must not trip, got %v", res.Signals)
	}
}
