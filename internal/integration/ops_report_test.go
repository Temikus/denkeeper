//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/opsreport"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

// The report's actual_fires query reads the engine's session/trigger audit
// event with source "scheduler" and detail.schedule_name. This test pins that
// contract end to end: if the engine changes the event, it fails here.
func TestOpsReport_ReadsWhatTheEngineWrites(t *testing.T) {
	h := chatToolHarness(t, []*llm.ChatResponse{
		{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function",
				Function: llm.FunctionCall{Name: "echo", Arguments: `{"input":"hi"}`}}},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content: "done", FinishReason: "stop",
			TokensUsed: llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:      "test-model",
		},
	})
	ctx := context.Background()

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "call echo", "session_id": "ops-report",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body.String())
	}

	cfg := scheduler.Config{
		Name: "nightly", Type: "agent", Schedule: "@every 24h", Agent: "default",
		Enabled: true, Headless: true,
	}
	if err := h.Scheduler.Register(cfg, func(scheduler.Entry) {}); err != nil {
		t.Fatal(err)
	}
	msg := configmcp.BuildScheduledMessage(cfg, agent.AdapterBinding{},
		configmcp.HeadlessConversationID(cfg.Name), time.Now(), time.Time{}, time.UTC)
	if err := h.Dispatcher.Agent("default").HandleMessage(ctx, msg); err != nil {
		t.Fatalf("scheduled turn: %v", err)
	}
	h.FlushAudit(t)

	rep, err := opsreport.New(h.Memory, h.KVStore, h.Scheduler, h.AuditStore).Build(ctx, opsreport.Query{
		Agent: "default", Now: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Spend.Turns != 2 {
		t.Errorf("spend turns = %d, want 2 (chat + scheduled)", rep.Spend.Turns)
	}
	if len(rep.Tools) != 1 || rep.Tools[0].ToolName != "echo" || rep.Tools[0].CallCount != 1 {
		t.Errorf("tools = %+v, want one echo call", rep.Tools)
	}
	if len(rep.Schedules) != 1 || rep.Schedules[0].ActualFires == nil || *rep.Schedules[0].ActualFires != 1 {
		t.Errorf("schedules = %+v, want nightly with actual_fires 1", rep.Schedules)
	}
}
