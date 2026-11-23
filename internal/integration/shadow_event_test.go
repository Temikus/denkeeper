//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
)

// shadowTurn is a supervised echo call the supervisor approves.
func shadowTurn() []*llm.ChatResponse {
	return []*llm.ChatResponse{
		{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID: "call_1", Type: "function",
				Function: llm.FunctionCall{Name: "echo", Arguments: `{"input":"shadowed"}`},
			}},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{Content: "APPROVE: matches the request", FinishReason: "stop", TokensUsed: llm.TokenUsage{Total: 10}, Model: "test-model"},
		{Content: "Tool returned: shadowed", FinishReason: "stop", TokensUsed: llm.TokenUsage{Total: 30}, Model: "test-model"},
	}
}

// streamApprovalEvents runs one SSE chat turn and returns its tool_approval
// events as raw JSON objects.
func streamApprovalEvents(t *testing.T, h *Harness) []map[string]json.RawMessage {
	t.Helper()
	req := h.AuthedRequest("POST", "/api/v1/chat", map[string]string{"message": "please call echo"})
	req.Header.Set("Accept", "text/event-stream")
	rec := h.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out []map[string]json.RawMessage
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var ev map[string]json.RawMessage
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatalf("SSE data is not JSON: %q", data)
		}
		if string(ev["type"]) == `"tool_approval"` {
			out = append(out, ev)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no tool_approval event in stream:\n%s", rec.Body.String())
	}
	return out
}

func TestShadowEvent_SSEStreamsShadowVerdict(t *testing.T) {
	h := supervisorHarness(t, shadowTurn())
	h.Dispatcher.Agent("default").SetSupervisorDecider(newJevDecider(denyingDecider{}), agent.DeciderStageConfig{
		Mode: "shadow", ApproveAt: 0.95, DenyAt: 0.05,
	})

	var found bool
	for _, ev := range streamApprovalEvents(t, h) {
		if string(ev["approval_status"]) != `"supervisor_approved"` {
			continue
		}
		found = true
		var shadow agent.ShadowVerdict
		if err := json.Unmarshal(ev["shadow"], &shadow); err != nil {
			t.Fatalf("shadow = %s: %v", ev["shadow"], err)
		}
		if shadow.Verdict != "DENY" || shadow.Agent != "default" || shadow.Decider != "jev" || shadow.Score == nil {
			t.Errorf("shadow = %+v", shadow)
		}
	}
	if !found {
		t.Fatal("no supervisor_approved event streamed")
	}
}

func TestShadowEvent_SSEOmitsShadowWithoutDecider(t *testing.T) {
	h := supervisorHarness(t, shadowTurn())

	for _, ev := range streamApprovalEvents(t, h) {
		if _, ok := ev["shadow"]; ok {
			t.Errorf("event %v has a shadow key with no decider", ev)
		}
	}
}
