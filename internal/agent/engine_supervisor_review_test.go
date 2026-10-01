package agent

import (
	"context"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

// The supervisor is a full agent whose router advertises its own tool
// catalogue; a review must not carry it.
func TestSupervisorReview_RequestCarriesNoTools(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	primary := &sequentialProvider{responses: toolCallThenDone()}
	supervisorProv := &capturingProvider{
		responses: []*llm.ChatResponse{
			{Content: "APPROVE: harmless search", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)
	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)
	supRouter.SetTools(func() []llm.ToolDef {
		return []llm.ToolDef{{Type: "function", Function: llm.FunctionDef{Name: "kv_set", Description: "write a key"}}}
	})

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "", nil, tool.NewManager(testLogger()), mgr, testLogger())
	supPerms, _ := security.NewPermissionEngine("autonomous")
	engine.SetSupervisor(NewEngine("argus", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger()))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-sup-notools",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "search",
		Timestamp:  time.Now(),
	}, nil); err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}

	if len(supervisorProv.requests) != 1 {
		t.Fatalf("supervisor received %d requests, want 1", len(supervisorProv.requests))
	}
	if got := len(supervisorProv.requests[0].Tools); got != 0 {
		t.Errorf("supervisor review request carried %d tool definitions, want 0", got)
	}
}
