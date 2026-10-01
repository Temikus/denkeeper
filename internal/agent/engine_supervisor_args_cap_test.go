package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

// toolCallWithArgs is toolCallThenDone with an arguments payload of exactly n
// bytes.
func toolCallWithArgs(n int) []*llm.ChatResponse {
	const prefix, suffix = `{"code":"`, `"}`
	args := prefix + strings.Repeat("x", n-len(prefix)-len(suffix)) + suffix
	resps := toolCallThenDone()
	resps[0].ToolCalls[0].Function = llm.FunctionCall{Name: "run_javascript", Arguments: args}
	return resps
}

func supervisorAuditDetail(t *testing.T, events []audit.Event) map[string]any {
	t.Helper()
	for i := range events {
		if events[i].Category != audit.CategorySupervisor {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(events[i].Detail), &detail); err != nil {
			t.Fatalf("audit detail not JSON: %v", err)
		}
		return detail
	}
	t.Fatal("no supervisor audit event emitted")
	return nil
}

func humanApprovalRequested(events []ChatEvent) bool {
	for _, evt := range events {
		if evt.Type == "tool_approval" && evt.ApprovalID != "" {
			return true
		}
	}
	return false
}

// Over the cap the review is guaranteed to fail (the prompt outgrows the
// supervisor's context window), so the LLM call is skipped and a human decides.
func TestSupervisorReview_OversizedArgsEscalateWithoutLLMCall(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallWithArgs(200), []*llm.ChatResponse{
		{Content: "APPROVE: fine", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
	})
	defer h.teardown()
	h.engine.SetSupervisorMaxArgsBytes(64)

	events := h.chat(t, "default:test:conv-big", "conv-big")

	if h.supProvider.callIndex != 0 {
		t.Errorf("supervisor LLM called %d times, want 0", h.supProvider.callIndex)
	}
	ev := findApprovalEvent(events, "supervisor_error")
	if ev == nil {
		t.Fatalf("statuses = %v, want supervisor_error", approvalStatuses(events))
	}
	if !strings.Contains(ev.Text, "too large") {
		t.Errorf("event text = %q, want it to say the arguments are too large", ev.Text)
	}
	if !humanApprovalRequested(events) {
		t.Errorf("no human approval requested; statuses = %v", approvalStatuses(events))
	}

	detail := supervisorAuditDetail(t, h.auditor.events)
	if detail["cause"] != "too_large" {
		t.Errorf("audit cause = %v, want \"too_large\"", detail["cause"])
	}
	if detail["arguments_bytes"] != float64(200) {
		t.Errorf("audit arguments_bytes = %v, want 200", detail["arguments_bytes"])
	}
	if detail["max_args_bytes"] != float64(64) {
		t.Errorf("audit max_args_bytes = %v, want 64", detail["max_args_bytes"])
	}
}

func TestSupervisorReview_ArgsAtCapAreReviewed(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallWithArgs(64), []*llm.ChatResponse{
		{Content: "APPROVE: fine", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
	})
	defer h.teardown()
	h.engine.SetSupervisorMaxArgsBytes(64)

	events := h.chat(t, "default:test:conv-cap", "conv-cap")

	if h.supProvider.callIndex != 1 {
		t.Errorf("supervisor LLM called %d times, want 1", h.supProvider.callIndex)
	}
	if findApprovalEvent(events, "supervisor_approved") == nil {
		t.Errorf("statuses = %v, want supervisor_approved", approvalStatuses(events))
	}
}

func TestSupervisorReview_DefaultCapApplies(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, toolCallWithArgs(defaultSupervisorMaxArgsBytes+1), []*llm.ChatResponse{
		{Content: "APPROVE: fine", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
	})
	defer h.teardown()

	events := h.chat(t, "default:test:conv-default", "conv-default")

	if h.supProvider.callIndex != 0 {
		t.Errorf("supervisor LLM called %d times, want 0", h.supProvider.callIndex)
	}
	if findApprovalEvent(events, "supervisor_error") == nil {
		t.Errorf("statuses = %v, want supervisor_error", approvalStatuses(events))
	}
}

// Zero must restore the default so a hot reload that clears the knob takes
// effect rather than keeping the previous override.
func TestEngine_SetSupervisorMaxArgsBytes_ZeroRestoresDefault(t *testing.T) {
	h := newSupervisorCostHarness(t, llm.SessionLimits{}, nil, nil)
	defer h.teardown()

	h.engine.SetSupervisorMaxArgsBytes(64)
	h.engine.SetSupervisorMaxArgsBytes(0)

	if h.engine.supervisorMaxArgsBytes != defaultSupervisorMaxArgsBytes {
		t.Errorf("supervisorMaxArgsBytes = %d, want default %d", h.engine.supervisorMaxArgsBytes, defaultSupervisorMaxArgsBytes)
	}
}

func TestSupervisorErrorCause_TooLarge(t *testing.T) {
	err := supervisorArgsTooLargeError(200, 64)
	if got := supervisorErrorCause(err); got != "too_large" {
		t.Errorf("supervisorErrorCause = %q, want %q", got, "too_large")
	}
}
