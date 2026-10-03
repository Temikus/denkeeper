//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

// denyingDecider answers every question with a confident "false".
type denyingDecider struct{}

func (denyingDecider) Decide(_ context.Context, req llm.DecisionRequest) (*llm.DecisionResponse, error) {
	answers := make(map[string]llm.Answer, len(req.Questions))
	for id := range req.Questions {
		answers[id] = llm.Answer{Type: llm.QuestionNoul, Noul: 0.01}
	}
	return &llm.DecisionResponse{Model: req.Model, Answers: answers, CostUSD: 0.0001}, nil
}

func newJevDecider(p llm.DecisionProvider) *llm.Decider {
	return llm.NewDecider(llm.DeciderConfig{
		Name: "jev", Provider: "or", Model: "typesafe/jev-1.13", Timeout: 5 * time.Second, MaxInputTokens: 30000,
	}, p, nil)
}

// supervisorAuditEvents returns the decider event and the LLM supervisor
// event of the run, either of which may be nil.
func supervisorAuditEvents(t *testing.T, h *Harness) (deciderEv, supervisorEv *audit.Event) {
	t.Helper()
	h.FlushAudit(t)
	events, _, err := h.AuditStore.List(context.Background(), audit.ListOpts{Categories: []string{audit.CategorySupervisor}})
	if err != nil {
		t.Fatalf("listing audit events: %v", err)
	}
	for i := range events {
		switch {
		case events[i].Source == "decider:jev":
			deciderEv = &events[i]
		case strings.HasPrefix(events[i].Source, "supervisor:"):
			supervisorEv = &events[i]
		}
	}
	return deciderEv, supervisorEv
}

// An enforcing decider in front of the supervisor denies on its own: the tool
// does not run and the supervisor is never asked.
func TestSupervisorDecider_EnforceDenyInFrontOfSupervisor(t *testing.T) {
	h := supervisorHarness(t, []*llm.ChatResponse{
		{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID: "call_1", Type: "function",
				Function: llm.FunctionCall{Name: "echo", Arguments: `{"input":"blocked"}`},
			}},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		// No supervisor verdict queued: a review would consume this reply.
		{Content: "The call was refused.", FinishReason: "stop", TokensUsed: llm.TokenUsage{Total: 30}, Model: "test-model"},
	})
	h.Dispatcher.Agent("default").SetSupervisorDecider(newJevDecider(denyingDecider{}), agent.DeciderStageConfig{
		Mode: agent.DeciderModeEnforce, ApproveAt: 0.95, DenyAt: 0.05,
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{"message": "please call echo"}))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if chatResp["response"] != "The call was refused." {
		t.Fatalf("response = %q, want the model's reply to the denial", chatResp["response"])
	}

	deciderEv, supervisorEv := supervisorAuditEvents(t, h)
	if supervisorEv != nil {
		t.Errorf("supervisor reviewed a call the decider had denied: %+v", supervisorEv)
	}
	if deciderEv == nil {
		t.Fatal("no decider audit event")
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(deciderEv.Detail), &detail); err != nil {
		t.Fatalf("decider detail not JSON: %v", err)
	}
	if detail["decision"] != "DENY" || detail["mode"] != "enforce" {
		t.Errorf("decider detail = %v, want decision DENY in enforce mode", detail)
	}
}

// A shadow decider in front of the supervisor is audited but does not change
// the outcome: it would deny, the supervisor approves, and the tool runs.
func TestSupervisorDecider_ShadowInFrontOfSupervisor(t *testing.T) {
	h := supervisorHarness(t, []*llm.ChatResponse{
		{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID: "call_1", Type: "function",
				Function: llm.FunctionCall{Name: "echo", Arguments: `{"input":"shadowed"}`},
			}},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{Content: "APPROVE: fine", FinishReason: "stop", TokensUsed: llm.TokenUsage{Total: 10}, Model: "test-model"},
		{Content: "Tool returned: shadowed", FinishReason: "stop", TokensUsed: llm.TokenUsage{Total: 30}, Model: "test-model"},
	})
	h.Dispatcher.Agent("default").SetSupervisorDecider(newJevDecider(denyingDecider{}), agent.DeciderStageConfig{
		Mode: "shadow", ApproveAt: 0.95, DenyAt: 0.05,
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{"message": "please call echo"}))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var chatResp map[string]string
	DecodeJSON(t, rec, &chatResp)
	if !strings.Contains(chatResp["response"], "shadowed") {
		t.Fatalf("tool did not run; response: %s", chatResp["response"])
	}

	deciderEv, supervisorEv := supervisorAuditEvents(t, h)
	if supervisorEv == nil {
		t.Error("no supervisor audit event: the supervisor must still review")
	}
	if deciderEv == nil {
		t.Fatal("no decider audit event")
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(deciderEv.Detail), &detail); err != nil {
		t.Fatalf("decider detail not JSON: %v", err)
	}
	if detail["decision"] != "shadow" || detail["would_decide"] != "DENY" {
		t.Errorf("decider detail = %v, want shadow / would_decide DENY", detail)
	}
}
