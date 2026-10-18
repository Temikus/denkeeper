package agent

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/llm/pricing"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

// Anthropic, OpenAI and Ollama report no cost, so the stored turn cost must
// come from the router's pricing lookup, summed across rounds.
func TestEngine_HandleMessage_RecordsRegistryCostWhenProviderReportsNone(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "shell", Arguments: "{}"}},
				},
				Model:        "test-model",
				TokensUsed:   llm.TokenUsage{Prompt: 1000, Completion: 100, Total: 1100},
				FinishReason: "tool_calls",
			},
			{
				Content:      "Done.",
				Model:        "test-model",
				TokensUsed:   llm.TokenUsage{Prompt: 2000, Completion: 200, Total: 2200},
				FinishReason: "stop",
			},
		},
	}

	reg := pricing.NewEmpty()
	reg.Register("test-model", pricing.ModelPrice{InputPerMTok: 2, OutputPerMTok: 10})
	router := llm.NewRouter("mock", "test-model", llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil))
	router.RegisterProvider(provider)
	router.SetPricing(reg)

	// restricted denies the unclassified "shell", so the turn takes two rounds.
	permissions, err := security.NewPermissionEngine("restricted")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	sent := &sentMessages{}
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "Test.", nil, nil, nil, testLogger())
	engine.tools = &tool.Manager{}

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-cost",
		UserID:     "user-1",
		Text:       "run a command",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	// (1000*2 + 100*10 + 2000*2 + 200*10) / 1e6
	const want = 0.009
	summary, err := store.GetTelemetrySummary(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("GetTelemetrySummary: %v", err)
	}
	if len(summary.ByModel) != 1 {
		t.Fatalf("by_model = %+v, want one row", summary.ByModel)
	}
	if got := summary.ByModel[0].TotalCost; math.Abs(got-want) > 1e-9 {
		t.Errorf("telemetry total_cost = %v, want %v", got, want)
	}
}
