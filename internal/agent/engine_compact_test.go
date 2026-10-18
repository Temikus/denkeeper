package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
)

// The summariser has nothing to call, and a request carrying tools would pay
// a prompt-cache write that is never read.
func TestCompactSession_SendsNoTools(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	const convID = "chan:main"
	if err := store.GetOrCreateConversationByID(ctx, convID, "telegram", "42"); err != nil {
		t.Fatalf("creating conversation: %v", err)
	}
	for _, m := range []StoredMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}} {
		if _, err := store.AddMessage(ctx, convID, m); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}

	provider := &sequentialProvider{responses: []*llm.ChatResponse{{Content: "They said hi.", FinishReason: "stop"}}}
	router := llm.NewRouter("mock", "test-model", llm.NewCostTracker(llm.SessionLimits{}, nil))
	router.RegisterProvider(provider)
	router.SetTools(func() []llm.ToolDef {
		return []llm.ToolDef{{Type: "function", Function: llm.FunctionDef{Name: "kv_get"}}}
	})
	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	engine := NewEngine("default", router, store, nil, permissions, nil, "Test.", nil, nil, nil, testLogger())

	summary, err := engine.CompactSession(ctx, convID)
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	if summary != "They said hi." {
		t.Errorf("summary = %q, want the model's summary", summary)
	}
	if len(provider.requests) != 1 || len(provider.requests[0].Tools) != 0 {
		t.Fatalf("requests = %d, tools on first = %v; want one request with no tools", len(provider.requests), provider.requests[0].Tools)
	}
	msgs, err := store.GetMessages(ctx, convID, 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0].Content, "[Session compacted]") {
		t.Errorf("history = %+v, want one compacted summary", msgs)
	}
}
