package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
)

// sentMessages collects outgoing messages from a SendFunc.
type sentMessages struct {
	msgs []adapter.OutgoingMessage
}

func (s *sentMessages) send(_ context.Context, msg adapter.OutgoingMessage) error {
	s.msgs = append(s.msgs, msg)
	return nil
}

// mockProvider implements llm.Provider for testing.
type mockProvider struct {
	response    *llm.ChatResponse
	err         error
	lastRequest *llm.ChatRequest
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	m.lastRequest = &req
	return m.response, m.err
}
func (m *mockProvider) HealthCheck(_ context.Context) error { return nil }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEngine_HandleMessage(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      "Hello from Denkeeper!",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	ctx := context.Background()
	msg := adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-123",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hi there",
		Timestamp:  time.Now(),
	}

	if err := engine.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	// Check response was sent
	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
	if sent.msgs[0].Text != "Hello from Denkeeper!" {
		t.Errorf("sent text = %q, want Hello from Denkeeper!", sent.msgs[0].Text)
	}
	if sent.msgs[0].ExternalID != "chat-123" {
		t.Errorf("sent external_id = %q, want chat-123", sent.msgs[0].ExternalID)
	}

	// Check messages were stored (namespaced: "default:test:chat-123")
	convID := "default:test:chat-123"
	messages, err := store.GetMessages(ctx, convID, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("stored %d messages, want 2", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "Hi there" {
		t.Errorf("message[0] = %+v, want user/Hi there", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "Hello from Denkeeper!" {
		t.Errorf("message[1] = %+v, want assistant/Hello from Denkeeper!", messages[1])
	}
}

func TestEngine_MultipleMessages_BuildsHistory(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Response",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	ctx := context.Background()

	// Send 3 messages
	for i := 0; i < 3; i++ {
		msg := adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-1",
			UserID:     "user-1",
			Text:       "Message " + string(rune('A'+i)),
			Timestamp:  time.Now(),
		}
		if err := engine.HandleMessage(ctx, msg); err != nil {
			t.Fatalf("HandleMessage %d: %v", i, err)
		}
	}

	// Should have 6 messages stored (3 user + 3 assistant)
	messages, err := store.GetMessages(ctx, "default:test:chat-1", 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(messages) != 6 {
		t.Errorf("stored %d messages, want 6", len(messages))
	}
}

func TestEngine_HandleMessage_PermissionDenied(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{Content: "should not be called"},
	})

	sent := &sentMessages{}

	// Create a permission engine that denies everything.
	permissions := security.NewDenyAll()

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hello",
		Timestamp:  time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for denied permission")
	}
	if len(sent.msgs) != 0 {
		t.Errorf("sent %d messages, want 0 (should not send on denied permission)", len(sent.msgs))
	}
}

func TestEngine_HandleMessage_LLMError(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		err: fmt.Errorf("LLM unavailable"),
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hello",
		Timestamp:  time.Now(),
	})
	if err == nil {
		t.Fatal("expected error when LLM fails")
	}
	if len(sent.msgs) != 0 {
		t.Errorf("sent %d messages, want 0 (should not send on LLM error)", len(sent.msgs))
	}
}

func TestEngine_HandleMessage_NilSendFunc(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Hello!",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// nil sendFunc — should not panic, message still processed and stored.
	engine := NewEngine("default", router, store, nil, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hello",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEngine_HandleMessage_EmptyText(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "I got an empty message",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	// Empty text should be handled gracefully
	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error on empty text: %v", err)
	}
	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
}

func TestEngine_HandleMessage_IsolatedSession(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Briefing delivered",
			TokensUsed: llm.TokenUsage{Total: 8},
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	ctx := context.Background()

	// Two isolated dispatches with distinct ConversationIDs.
	msg1 := adapter.IncomingMessage{
		Adapter:        "telegram",
		ExternalID:     "12345",
		UserName:       "scheduler",
		Text:           "[Scheduled: daily-briefing]",
		Timestamp:      time.Now(),
		ConversationID: "sched:daily-briefing:1000",
	}
	msg2 := adapter.IncomingMessage{
		Adapter:        "telegram",
		ExternalID:     "12345",
		UserName:       "scheduler",
		Text:           "[Scheduled: daily-briefing]",
		Timestamp:      time.Now(),
		ConversationID: "sched:daily-briefing:2000",
	}

	if err := engine.HandleMessage(ctx, msg1); err != nil {
		t.Fatalf("HandleMessage msg1: %v", err)
	}
	if err := engine.HandleMessage(ctx, msg2); err != nil {
		t.Fatalf("HandleMessage msg2: %v", err)
	}

	// Each isolated session has its own conversation with exactly 2 messages.
	msgs1, err := store.GetMessages(ctx, "sched:daily-briefing:1000", 100)
	if err != nil {
		t.Fatalf("GetMessages conv1: %v", err)
	}
	msgs2, err := store.GetMessages(ctx, "sched:daily-briefing:2000", 100)
	if err != nil {
		t.Fatalf("GetMessages conv2: %v", err)
	}

	if len(msgs1) != 2 {
		t.Errorf("conv1 has %d messages, want 2 (isolated)", len(msgs1))
	}
	if len(msgs2) != 2 {
		t.Errorf("conv2 has %d messages, want 2 (isolated)", len(msgs2))
	}

	// The channel's regular conversation is untouched.
	sharedMsgs, err := store.GetMessages(ctx, "default:telegram:12345", 100)
	if err != nil {
		t.Fatalf("GetMessages shared: %v", err)
	}
	if len(sharedMsgs) != 0 {
		t.Errorf("shared conversation has %d messages, want 0", len(sharedMsgs))
	}
}

func TestEngine_HandleMessage_CustomSystemPrompt(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	mp := &mockProvider{
		response: &llm.ChatResponse{
			Content:    "OK",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(mp)

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	customPrompt := "You are a custom persona with special instructions."
	engine := NewEngine("default", router, store, sent.send, permissions, nil, customPrompt, nil, nil, nil, testLogger())

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hello",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if mp.lastRequest == nil {
		t.Fatal("provider was not called")
	}
	if len(mp.lastRequest.Messages) == 0 {
		t.Fatal("no messages in request")
	}
	if mp.lastRequest.Messages[0].Role != "system" {
		t.Errorf("first message role = %q, want system", mp.lastRequest.Messages[0].Role)
	}
	if !strings.HasPrefix(mp.lastRequest.Messages[0].Content, customPrompt) {
		t.Errorf("system prompt = %q, want prefix %q", mp.lastRequest.Messages[0].Content, customPrompt)
	}
}

func TestSanitizeStaleDirectives_StripsTag(t *testing.T) {
	text := "Here is my answer.\n\n[MEMORY_UPDATE]\nUser prefers concise answers.\n[/MEMORY_UPDATE]"
	cleaned := sanitizeStaleDirectives(text, testLogger())
	if cleaned != "Here is my answer." {
		t.Errorf("cleaned = %q, want %q", cleaned, "Here is my answer.")
	}
}

func TestSanitizeStaleDirectives_NoTag(t *testing.T) {
	text := "Just a normal response."
	cleaned := sanitizeStaleDirectives(text, testLogger())
	if cleaned != text {
		t.Errorf("cleaned = %q, want original text", cleaned)
	}
}

func TestSanitizeStaleDirectives_MissingCloseTag(t *testing.T) {
	text := "Answer.\n\n[MEMORY_UPDATE]\nSome content without close tag."
	cleaned := sanitizeStaleDirectives(text, testLogger())
	if cleaned != text {
		t.Errorf("cleaned should be unchanged when close tag is missing")
	}
}

func TestSanitizeStaleDirectives_MultipleTags(t *testing.T) {
	text := "Before.\n\n[MEMORY_UPDATE]\nMemory.\n[/MEMORY_UPDATE]\n\nMiddle.\n\n[SOUL_UPDATE]\nSoul.\n[/SOUL_UPDATE]\n\nAfter."
	cleaned := sanitizeStaleDirectives(text, testLogger())
	if strings.Contains(cleaned, "MEMORY_UPDATE") || strings.Contains(cleaned, "SOUL_UPDATE") {
		t.Errorf("cleaned should not contain directive tags, got %q", cleaned)
	}
	if !strings.Contains(cleaned, "Before.") || !strings.Contains(cleaned, "After.") || !strings.Contains(cleaned, "Middle.") {
		t.Errorf("cleaned should preserve surrounding text, got %q", cleaned)
	}
}

func TestEngine_HandleMessage_StaleDirectiveStripped(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Hello!\n\n[MEMORY_UPDATE]\nShould be stripped.\n[/MEMORY_UPDATE]",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// Stale directive tags should be stripped from user-facing message.
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "Fallback.", nil, nil, nil, testLogger())

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		Text:       "Hi",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if sent.msgs[0].Text != "Hello!" {
		t.Errorf("sent text = %q, want %q", sent.msgs[0].Text, "Hello!")
	}
}

// sequentialProvider returns responses in order for successive calls.
type sequentialProvider struct {
	responses []*llm.ChatResponse
	callIndex int
}

func (s *sequentialProvider) Name() string { return "mock" }
func (s *sequentialProvider) ChatCompletion(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if s.callIndex >= len(s.responses) {
		return nil, fmt.Errorf("no more mock responses (call %d)", s.callIndex)
	}
	resp := s.responses[s.callIndex]
	s.callIndex++
	return resp, nil
}
func (s *sequentialProvider) HealthCheck(_ context.Context) error { return nil }

func TestEngine_HandleMessage_ToolCallNoManager(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "get_weather",
							Arguments: `{"city":"London"}`,
						},
					},
				},
				TokensUsed:   llm.TokenUsage{Total: 20},
				FinishReason: "tool_calls",
			},
			{
				Content:      "The weather in London is sunny, 22°C.",
				TokensUsed:   llm.TokenUsage{Total: 15},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	// LLM requests tools but no tool manager — should error.
	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-tool-1",
		UserID:     "user-1",
		Text:       "What's the weather?",
		Timestamp:  time.Now(),
	})
	if err == nil {
		t.Fatal("expected error when LLM requests tools but no tool manager")
	}
	if !strings.Contains(err.Error(), "no tool manager configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEngine_HandleMessage_ToolCallDenied(t *testing.T) {
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
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	sent := &sentMessages{}

	// restricted tier does NOT have use_tools permission.
	permissions, err := security.NewPermissionEngine("restricted")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())
	engine.tools = &tool.Manager{} // non-nil so we reach the permission check

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-denied",
		UserID:     "user-1",
		Text:       "run a command",
		Timestamp:  time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for denied tool execution")
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEngine_SupervisedToolCallApproval_Approved(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "Here are the search results.",
				TokensUsed:   llm.TokenUsage{Total: 20},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// Create a mock tool manager that returns a result.
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start the message handling in a goroutine since it will block on approval.
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessage(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-supervised",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		})
	}()

	// Wait a bit for the approval to be submitted, then approve it.
	time.Sleep(100 * time.Millisecond)
	approvals, listErr := mgr.List(ctx, approval.StatusPending)
	if listErr != nil {
		t.Fatalf("listing approvals: %v", listErr)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(approvals))
	}
	if approvals[0].Kind != approval.ActionKindToolCall {
		t.Errorf("approval kind = %q, want %q", approvals[0].Kind, approval.ActionKindToolCall)
	}

	// Approve it.
	_, resolveErr := mgr.Resolve(ctx, approvals[0].ID, true, "test-operator")
	if resolveErr != nil {
		t.Fatalf("resolving approval: %v", resolveErr)
	}

	// Wait for the pipeline to complete.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for HandleMessage")
	}

	// The response should contain the search results text.
	if len(sent.msgs) < 1 {
		t.Fatal("expected at least 1 sent message")
	}
	if !strings.Contains(sent.msgs[len(sent.msgs)-1].Text, "search results") {
		t.Errorf("response = %q, want to contain 'search results'", sent.msgs[len(sent.msgs)-1].Text)
	}
}

func TestEngine_SupervisedToolCallApproval_Denied(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "I was unable to perform the search.",
				TokensUsed:   llm.TokenUsage{Total: 15},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessage(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-denied",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		})
	}()

	// Wait for approval, then deny it.
	time.Sleep(100 * time.Millisecond)
	approvals, _ := mgr.List(ctx, approval.StatusPending)
	if len(approvals) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(approvals))
	}

	_, _ = mgr.Resolve(ctx, approvals[0].ID, false, "test-operator")

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for HandleMessage")
	}

	// The LLM should have received the denial message and responded.
	if len(sent.msgs) < 1 {
		t.Fatal("expected at least 1 sent message")
	}
}

func TestEngine_ApprovalTimeout_RetryThenApproved(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "Here are the search results.",
				TokensUsed:   llm.TokenUsage{Total: 15},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())
	engine.SetApprovalConfig(150*time.Millisecond, 1) // short timeout, 1 retry

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessage(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-retry",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		})
	}()

	// Let first attempt time out, then approve the retry.
	time.Sleep(250 * time.Millisecond)
	approvals, _ := mgr.List(ctx, approval.StatusPending)
	if len(approvals) < 1 {
		t.Fatal("expected at least 1 pending approval after retry")
	}
	// Approve the latest one.
	_, _ = mgr.Resolve(ctx, approvals[len(approvals)-1].ID, true, "test-operator")

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for HandleMessage")
	}

	if len(sent.msgs) < 1 {
		t.Fatal("expected at least 1 sent message")
	}
}

func TestEngine_ApprovalTimeout_RetriesExhausted(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "The tool timed out.",
				TokensUsed:   llm.TokenUsage{Total: 15},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())
	engine.SetApprovalConfig(100*time.Millisecond, 1) // short timeout, 1 retry — both will time out

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = engine.HandleMessage(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-exhausted",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "search for test",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	// The LLM should have received the timeout and produced a response.
	if len(sent.msgs) < 1 {
		t.Fatal("expected at least 1 sent message")
	}
}

func TestEngine_ApprovalDenied_NoRetry(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "Denied.",
				TokensUsed:   llm.TokenUsage{Total: 15},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())
	engine.SetApprovalConfig(5*time.Second, 2) // retries configured but denial should NOT retry

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessage(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-deny-no-retry",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		})
	}()

	// Deny immediately.
	time.Sleep(100 * time.Millisecond)
	approvals, _ := mgr.List(ctx, approval.StatusPending)
	if len(approvals) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(approvals))
	}
	_, _ = mgr.Resolve(ctx, approvals[0].ID, false, "test-operator")

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for HandleMessage")
	}

	// Should only have 1 approval total (no retry on denial).
	allApprovals, _ := mgr.List(ctx, "")
	pendingCount := 0
	for _, a := range allApprovals {
		if a.Status == approval.StatusPending {
			pendingCount++
		}
	}
	if pendingCount > 0 {
		t.Errorf("expected 0 pending approvals after denial, got %d", pendingCount)
	}
}

func TestBuildSystemPrompt_IncludesSessionContext(t *testing.T) {
	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", nil, nil, nil, permissions, nil, "Base prompt.", nil, nil, nil, testLogger())

	prompt := engine.buildSystemPrompt(permissions, adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "387956986",
	})
	if !strings.Contains(prompt, "telegram:387956986") {
		t.Error("system prompt should contain the delivery channel")
	}
	if !strings.Contains(prompt, "Session Context") {
		t.Error("system prompt should contain Session Context section")
	}
}

func TestBuildSystemPrompt_NoAdapterOmitsContext(t *testing.T) {
	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", nil, nil, nil, permissions, nil, "Base prompt.", nil, nil, nil, testLogger())

	prompt := engine.buildSystemPrompt(permissions, adapter.IncomingMessage{})
	if strings.Contains(prompt, "Session Context") {
		t.Error("system prompt should NOT contain Session Context when adapter is empty")
	}
}

func TestEngine_HandleMessage_SessionTierOverride(t *testing.T) {
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
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	sent := &sentMessages{}

	// Global tier is "supervised" (allows use_tools).
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())
	engine.tools = &tool.Manager{} // non-nil so we reach the permission check

	// Override to "restricted" via SessionTier — should deny tool use.
	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:     "test",
		ExternalID:  "chat-tier-override",
		UserID:      "user-1",
		UserName:    "scheduler",
		Text:        "[Scheduled: daily-briefing]",
		Timestamp:   time.Now(),
		SessionTier: "restricted",
	})
	if err == nil {
		t.Fatal("expected error for restricted tier denying tool use")
	}
	if !strings.Contains(err.Error(), "not permitted") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEngine_HandleMessage_SessionTierEmpty_UsesGlobal(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "get_weather", Arguments: `{"city":"London"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "Sunny in London!",
				TokensUsed:   llm.TokenUsage{Total: 15},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	sent := &sentMessages{}

	// Global tier is "supervised" (allows use_tools).
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, toolMgr, nil, testLogger())

	// Empty SessionTier — should use global "supervised" and allow tool calls.
	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-global-tier",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "What's the weather?",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
	if sent.msgs[0].Text != "Sunny in London!" {
		t.Errorf("sent text = %q, want %q", sent.msgs[0].Text, "Sunny in London!")
	}
}

func TestEngine_HandleMessage_SessionTierInvalid_FallsBack(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Response",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())

	// Invalid SessionTier — should log warning and fall back to global.
	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:     "test",
		ExternalID:  "chat-invalid-tier",
		UserID:      "user-1",
		UserName:    "testuser",
		Text:        "Hello",
		Timestamp:   time.Now(),
		SessionTier: "bogus",
	})
	if err != nil {
		t.Fatalf("unexpected error with invalid tier: %v", err)
	}
	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
}

func TestEngine_HandleMessage_VoiceFlagPropagated(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      "Voice response",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "Test.", nil, nil, nil, testLogger())

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-voice",
		UserID:     "user-1",
		UserName:   "voiceuser",
		Text:       "transcribed voice text",
		Timestamp:  time.Now(),
		IsVoice:    true,
	})
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
	if !sent.msgs[0].IsVoice {
		t.Error("outgoing message IsVoice should be true when incoming was voice")
	}
}

func TestEngine_Name(t *testing.T) {
	engine := NewEngine("work-assistant", nil, nil, nil, nil, nil, "", nil, nil, nil, testLogger())
	if engine.Name() != "work-assistant" {
		t.Errorf("Name() = %q, want work-assistant", engine.Name())
	}
}

func TestEngine_ConversationNamespacing(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Response",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// Two engines with different names, same store.
	engine1 := NewEngine("agent-a", router, store, sent.send, permissions, nil, "Agent A.", nil, nil, nil, testLogger())
	engine2 := NewEngine("agent-b", router, store, sent.send, permissions, nil, "Agent B.", nil, nil, nil, testLogger())

	ctx := context.Background()
	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		UserID:     "user-1",
		Text:       "Hello",
		Timestamp:  time.Now(),
	}

	if err := engine1.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("engine1 HandleMessage: %v", err)
	}
	if err := engine2.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("engine2 HandleMessage: %v", err)
	}

	// Each agent should have its own conversation.
	msgs1, _ := store.GetMessages(ctx, "agent-a:telegram:12345", 100)
	msgs2, _ := store.GetMessages(ctx, "agent-b:telegram:12345", 100)

	if len(msgs1) != 2 {
		t.Errorf("agent-a has %d messages, want 2", len(msgs1))
	}
	if len(msgs2) != 2 {
		t.Errorf("agent-b has %d messages, want 2", len(msgs2))
	}
}

// ---------------------------------------------------------------------------
// Accessor tests: PersonaDir, PersonaSections, ToolNames
// ---------------------------------------------------------------------------

func TestEngine_PersonaDir_NoPersona(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	if dir := eng.PersonaDir(); dir != "" {
		t.Errorf("PersonaDir() = %q, want empty string", dir)
	}
}

func TestEngine_PersonaDir_WithPersona(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are helpful."), 0600); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, p, "", nil, nil, nil, testLogger())

	if got := eng.PersonaDir(); got != dir {
		t.Errorf("PersonaDir() = %q, want %q", got, dir)
	}
}

func TestEngine_PersonaSections_NoPersona(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	if secs := eng.PersonaSections(); secs != nil {
		t.Errorf("PersonaSections() = %v, want nil", secs)
	}
}

func TestEngine_PersonaSections_SoulOnly(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are helpful."), 0600); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, p, "", nil, nil, nil, testLogger())

	secs := eng.PersonaSections()
	if secs == nil {
		t.Fatal("PersonaSections() = nil, want map")
	}
	if !secs["soul"] {
		t.Error("soul should be loaded")
	}
	if secs["user"] {
		t.Error("user should not be loaded (no USER.md)")
	}
	if secs["memory"] {
		t.Error("memory should not be loaded (no MEMORY.md)")
	}
}

func TestEngine_PersonaSections_AllSections(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	for _, f := range []struct{ name, body string }{
		{"SOUL.md", "You are helpful."},
		{"USER.md", "User context."},
		{"MEMORY.md", "Memory content."},
	} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), 0600); err != nil {
			t.Fatalf("writing %s: %v", f.name, err)
		}
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, p, "", nil, nil, nil, testLogger())

	secs := eng.PersonaSections()
	if secs == nil {
		t.Fatal("PersonaSections() = nil, want map")
	}
	for _, sec := range []string{"soul", "user", "memory"} {
		if !secs[sec] {
			t.Errorf("section %q should be loaded", sec)
		}
	}
}

func TestEngine_PersonaSection_Success(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are helpful."), 0600)
	_ = os.WriteFile(filepath.Join(dir, "USER.md"), []byte("User info."), 0600)
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("Memory data."), 0600)
	p, _ := persona.Load(dir)

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, p, "", nil, nil, nil, testLogger())

	content, editable, agentMutable, ok := eng.PersonaSection("soul")
	if !ok {
		t.Fatal("PersonaSection('soul') returned ok=false")
	}
	if content != "You are helpful." {
		t.Errorf("soul content = %q, want %q", content, "You are helpful.")
	}
	if !editable {
		t.Error("soul should be editable by user")
	}
	if !agentMutable {
		t.Error("soul should be agent-mutable")
	}

	content, editable, agentMutable, ok = eng.PersonaSection("memory")
	if !ok {
		t.Fatal("PersonaSection('memory') returned ok=false")
	}
	if content != "Memory data." {
		t.Errorf("memory content = %q, want %q", content, "Memory data.")
	}
	if !editable {
		t.Error("memory should be editable by user")
	}
	if !agentMutable {
		t.Error("memory should be agent-mutable")
	}
}

func TestEngine_PersonaSection_NoPersona(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	_, _, _, ok := eng.PersonaSection("soul")
	if ok {
		t.Error("PersonaSection should return ok=false when persona is nil")
	}
}

func TestEngine_PersonaSection_UnknownSection(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test."), 0600)
	p, _ := persona.Load(dir)

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, p, "", nil, nil, nil, testLogger())

	_, _, _, ok := eng.PersonaSection("evil")
	if ok {
		t.Error("PersonaSection should return ok=false for unknown section")
	}
}

func TestEngine_SavePersonaSection_Success(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test."), 0600)
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("Old."), 0600)
	p, _ := persona.Load(dir)

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, p, "", nil, nil, nil, testLogger())

	if err := eng.SavePersonaSection("memory", "Updated."); err != nil {
		t.Fatalf("SavePersonaSection: %v", err)
	}

	content, _, _, ok := eng.PersonaSection("memory")
	if !ok {
		t.Fatal("PersonaSection after save returned ok=false")
	}
	if content != "Updated." {
		t.Errorf("content = %q, want %q", content, "Updated.")
	}
}

func TestEngine_SavePersonaSection_NoPersona(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	if err := eng.SavePersonaSection("memory", "data"); err == nil {
		t.Error("SavePersonaSection should return error when persona is nil")
	}
}

func TestEngine_ToolNames_NoTools(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	if names := eng.ToolNames(); names != nil {
		t.Errorf("ToolNames() = %v, want nil", names)
	}
}

func TestEngine_ToolNames_WithToolManager(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	mgr := tool.NewManager(testLogger())
	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, mgr, nil, testLogger())

	// An empty tool manager → has tools configured but no tools discovered yet.
	if eng.ToolNames() == nil {
		t.Error("ToolNames() should return non-nil slice when tool manager is set")
	}
	if len(eng.ToolNames()) != 0 {
		t.Errorf("ToolNames() = %v, want empty slice", eng.ToolNames())
	}
}

// ---------------------------------------------------------------------------
// Skill mutation methods
// ---------------------------------------------------------------------------

func TestEngine_GetSkill(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback",
		[]skill.Skill{
			{Name: "greet", Description: "Greeting", Version: "1.0"},
			{Name: "help", Description: "Help system", Version: "2.0"},
		}, nil, nil, testLogger())

	sk, ok := eng.GetSkill("greet")
	if !ok {
		t.Fatal("GetSkill should find 'greet'")
	}
	if sk.Name != "greet" || sk.Version != "1.0" {
		t.Errorf("GetSkill returned %+v, want greet/1.0", sk)
	}
}

func TestEngine_GetSkill_NotFound(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback",
		[]skill.Skill{{Name: "greet"}}, nil, nil, testLogger())

	_, ok := eng.GetSkill("nonexistent")
	if ok {
		t.Error("GetSkill should return false for nonexistent skill")
	}
}

func TestEngine_UpdateSkill(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback",
		[]skill.Skill{{Name: "greet", Version: "1.0"}}, nil, nil, testLogger())

	ok := eng.UpdateSkill("greet", skill.Skill{Name: "greet", Version: "2.0", Description: "Updated"})
	if !ok {
		t.Fatal("UpdateSkill should return true for existing skill")
	}

	sk, found := eng.GetSkill("greet")
	if !found {
		t.Fatal("skill should still exist after update")
	}
	if sk.Version != "2.0" || sk.Description != "Updated" {
		t.Errorf("updated skill = %+v, want version 2.0 / Updated", sk)
	}
}

func TestEngine_UpdateSkill_NotFound(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	ok := eng.UpdateSkill("nonexistent", skill.Skill{Name: "nonexistent"})
	if ok {
		t.Error("UpdateSkill should return false for nonexistent skill")
	}
}

func TestEngine_RemoveSkill(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback",
		[]skill.Skill{
			{Name: "greet"},
			{Name: "help"},
		}, nil, nil, testLogger())

	ok := eng.RemoveSkill("greet")
	if !ok {
		t.Fatal("RemoveSkill should return true for existing skill")
	}

	_, found := eng.GetSkill("greet")
	if found {
		t.Error("skill should not exist after removal")
	}

	// Other skill should remain.
	_, found = eng.GetSkill("help")
	if !found {
		t.Error("other skill should still exist")
	}
}

func TestEngine_RemoveSkill_NotFound(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	ok := eng.RemoveSkill("nonexistent")
	if ok {
		t.Error("RemoveSkill should return false for nonexistent skill")
	}
}

func TestEngine_SetPermissionTier(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("supervised")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	if eng.PermissionTier() != "supervised" {
		t.Fatalf("initial tier = %q, want supervised", eng.PermissionTier())
	}

	if err := eng.SetPermissionTier("autonomous"); err != nil {
		t.Fatalf("SetPermissionTier: %v", err)
	}
	if eng.PermissionTier() != "autonomous" {
		t.Errorf("tier after set = %q, want autonomous", eng.PermissionTier())
	}

	if err := eng.SetPermissionTier("restricted"); err != nil {
		t.Fatalf("SetPermissionTier restricted: %v", err)
	}
	if eng.PermissionTier() != "restricted" {
		t.Errorf("tier after set = %q, want restricted", eng.PermissionTier())
	}
}

func TestEngine_SetPermissionTier_Invalid(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("supervised")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	if err := eng.SetPermissionTier("superuser"); err == nil {
		t.Fatal("expected error for invalid tier")
	}
	// Original tier should be preserved after invalid set.
	if eng.PermissionTier() != "supervised" {
		t.Errorf("tier = %q after invalid set, want supervised (unchanged)", eng.PermissionTier())
	}
}

func TestEngine_SetModel(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())

	if eng.ModelName() != "test-model" {
		t.Fatalf("initial model = %q, want test-model", eng.ModelName())
	}

	eng.SetModel("new-model-v2")
	if eng.ModelName() != "new-model-v2" {
		t.Errorf("model after set = %q, want new-model-v2", eng.ModelName())
	}
}

func TestEngine_SkillsDir(t *testing.T) {
	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{response: &llm.ChatResponse{Content: "ok"}})
	perms, _ := security.NewPermissionEngine("autonomous")

	eng := NewEngine("default", router, store, nil, perms, nil, "fallback", nil, nil, nil, testLogger())
	eng.SetSkillDirs("/tmp/agent-skills", "/tmp/global-skills")

	if eng.SkillsDir() != "/tmp/agent-skills" {
		t.Errorf("SkillsDir() = %q, want /tmp/agent-skills", eng.SkillsDir())
	}
}

func TestEngine_DisplayName_NoPersona(t *testing.T) {
	eng := NewEngine("my-agent", nil, nil, nil, nil, nil, "", nil, nil, nil, testLogger())
	if got := eng.DisplayName(); got != "my-agent" {
		t.Errorf("DisplayName() = %q, want 'my-agent'", got)
	}
}

func TestEngine_DisplayName_WithIdentityName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are helpful."), 0600); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte("---\nname: Moltis\n---\n"), 0600); err != nil {
		t.Fatalf("writing IDENTITY.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}
	eng := NewEngine("my-agent", nil, nil, nil, nil, p, "", nil, nil, nil, testLogger())
	if got := eng.DisplayName(); got != "Moltis" {
		t.Errorf("DisplayName() = %q, want 'Moltis'", got)
	}
}

func TestEngine_DisplayName_WithIdentityNameAndEmoji(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are helpful."), 0600); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte("---\nname: Moltis\nemoji: \"🧊\"\n---\n"), 0600); err != nil {
		t.Fatalf("writing IDENTITY.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}
	eng := NewEngine("my-agent", nil, nil, nil, nil, p, "", nil, nil, nil, testLogger())
	if got := eng.DisplayName(); got != "🧊 Moltis" {
		t.Errorf("DisplayName() = %q, want '🧊 Moltis'", got)
	}
}

func TestEngine_SoftLimitBreaksToolLoop(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Provider returns tool_calls three times, then stop.
	// With soft limit hit after round 1, we should only see 1 tool round.
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls:    []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "get_weather", Arguments: `{"city":"London"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				// Second LLM call (after tool execution) — still wants more tools.
				ToolCalls:    []llm.ToolCall{{ID: "call_2", Type: "function", Function: llm.FunctionCall{Name: "get_time", Arguments: `{}`}}},
				Content:      "Intermediate result.",
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "Final answer.",
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "stop",
			},
		},
	}

	// Soft limit = 0.001 USD. Pre-seed cost to exceed it.
	costTracker := llm.NewCostTracker(llm.SessionLimits{Soft: 0.001}, nil)
	convID := "default:test:chat-softlimit"
	costTracker.Record(convID, 0.01) // already over soft limit

	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "You are a test assistant.", nil, toolMgr, nil, testLogger())

	var events []ChatEvent
	onEvent := func(evt ChatEvent) {
		events = append(events, evt)
	}

	text, err := engine.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-softlimit",
		UserID:     "user-1",
		Text:       "What's the weather?",
		Timestamp:  time.Now(),
	}, onEvent)
	if err != nil {
		t.Fatalf("soft limit should not return error, got: %v", err)
	}

	// The loop should have broken after round 1 due to soft limit.
	// The response should contain the intermediate content from the second response.
	if text == "" {
		t.Error("expected non-empty response text")
	}

	// Verify cost_limit event was emitted.
	var foundCostLimit bool
	for _, evt := range events {
		if evt.Type == "cost_limit" {
			foundCostLimit = true
			if !strings.Contains(evt.Text, "approaching cost limit") {
				t.Errorf("cost_limit event text = %q, want contains 'approaching cost limit'", evt.Text)
			}
		}
	}
	if !foundCostLimit {
		t.Errorf("expected cost_limit event in events: %+v", events)
	}

	// Provider should have been called exactly 2 times (initial + 1 tool round),
	// not 3 (would mean soft limit didn't break the loop).
	if provider.callIndex != 2 {
		t.Errorf("provider called %d times, want 2 (soft limit should break loop after round 1)", provider.callIndex)
	}
}

func TestEngine_NoLimitCompletesToolLoop(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls:    []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "get_weather", Arguments: `{"city":"London"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				ToolCalls:    []llm.ToolCall{{ID: "call_2", Type: "function", Function: llm.FunctionCall{Name: "get_time", Arguments: `{}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "London is sunny and it's 3pm.",
				TokensUsed:   llm.TokenUsage{Total: 15},
				FinishReason: "stop",
			},
		},
	}

	// Zero limits = disabled.
	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "You are a test assistant.", nil, toolMgr, nil, testLogger())

	text, err := engine.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-nolimit",
		UserID:     "user-1",
		Text:       "What's the weather and time?",
		Timestamp:  time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if text != "London is sunny and it's 3pm." {
		t.Errorf("response = %q, want %q", text, "London is sunny and it's 3pm.")
	}

	// All 3 provider responses should have been consumed (2 tool rounds + final).
	if provider.callIndex != 3 {
		t.Errorf("provider called %d times, want 3 (all rounds completed)", provider.callIndex)
	}
}

func TestEngine_DisplayName_WithPersonaNoIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are helpful."), 0600); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}
	eng := NewEngine("my-agent", nil, nil, nil, nil, p, "", nil, nil, nil, testLogger())
	if got := eng.DisplayName(); got != "my-agent" {
		t.Errorf("DisplayName() = %q, want 'my-agent'", got)
	}
}

// streamingCancelProvider is a streaming mock that emits content_delta chunks,
// then cancels the provided cancel func and returns context.Canceled — simulating
// a client disconnect that occurs while the LLM is still streaming.
type streamingCancelProvider struct {
	chunks []string
	cancel context.CancelFunc // called after all chunks are emitted
}

func (p *streamingCancelProvider) Name() string                        { return "streaming-cancel-mock" }
func (p *streamingCancelProvider) SupportsStreaming() bool             { return true }
func (p *streamingCancelProvider) HealthCheck(_ context.Context) error { return nil }
func (p *streamingCancelProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if req.OnStream != nil {
		for _, c := range p.chunks {
			req.OnStream(llm.StreamChunk{ContentDelta: c})
		}
	}
	// Simulate disconnect: cancel the context then report it as the error.
	if p.cancel != nil {
		p.cancel()
	}
	return nil, context.Canceled
}

// TestEngine_ChatWithEvents_PartialResponseSavedOnContextCancel verifies that
// accumulated streaming content is stored in the DB when the context is
// cancelled mid-stream (e.g. WebSocket client disconnects).
func TestEngine_ChatWithEvents_PartialResponseSavedOnContextCancel(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Context starts valid so DB setup succeeds; the provider cancels it
	// after emitting chunks, before returning.
	ctx, cancel := context.WithCancel(context.Background())

	provider := &streamingCancelProvider{
		chunks: []string{"Hello", " from", " partial"},
		cancel: cancel,
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("streaming-cancel-mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	engine := NewEngine("default", router, store, nil, permissions, nil, "sys", nil, nil, nil, testLogger())

	sessionID := "partial-save-session"
	msg := adapter.IncomingMessage{
		Adapter:        "ws",
		ExternalID:     sessionID,
		ConversationID: sessionID,
		Text:           "Hi",
		Timestamp:      time.Now(),
	}

	_, err = engine.ChatWithEvents(ctx, msg, func(ChatEvent) {})
	// Expect an error because the context was cancelled.
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}

	// Give the background save a moment to complete.
	time.Sleep(100 * time.Millisecond)

	// The partial streamed content must be persisted.
	msgs, err := store.GetMessages(context.Background(), sessionID, 10)
	if err != nil {
		t.Fatalf("getting messages: %v", err)
	}

	var assistantMsgs []StoredMessage
	for _, m := range msgs {
		if m.Role == "assistant" {
			assistantMsgs = append(assistantMsgs, m)
		}
	}
	if len(assistantMsgs) != 1 {
		t.Fatalf("got %d assistant messages, want 1", len(assistantMsgs))
	}
	if assistantMsgs[0].Content != "Hello from partial" {
		t.Errorf("partial content = %q, want %q", assistantMsgs[0].Content, "Hello from partial")
	}
}

// TestEngine_ChatWithEvents_NoPartialSaveWhenNoContent verifies that no
// assistant message is written when the context is cancelled before any
// streaming content was received.
func TestEngine_ChatWithEvents_NoPartialSaveWhenNoContent(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithCancel(context.Background())

	provider := &streamingCancelProvider{
		chunks: nil, // no chunks emitted before cancel
		cancel: cancel,
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("streaming-cancel-mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	engine := NewEngine("default", router, store, nil, permissions, nil, "sys", nil, nil, nil, testLogger())

	sessionID := "no-content-cancel-session"
	msg := adapter.IncomingMessage{
		Adapter:        "ws",
		ExternalID:     sessionID,
		ConversationID: sessionID,
		Text:           "Hi",
		Timestamp:      time.Now(),
	}

	_, err = engine.ChatWithEvents(ctx, msg, func(ChatEvent) {})
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}

	time.Sleep(100 * time.Millisecond)

	msgs, err := store.GetMessages(context.Background(), sessionID, 10)
	if err != nil {
		t.Fatalf("getting messages: %v", err)
	}

	for _, m := range msgs {
		if m.Role == "assistant" {
			t.Errorf("unexpected assistant message stored when no content was streamed: %q", m.Content)
		}
	}
}
