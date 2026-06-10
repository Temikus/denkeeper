package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
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

// collectingAuditor collects emitted audit events for test assertions.
type collectingAuditor struct {
	events []audit.Event
}

func (a *collectingAuditor) Emit(_ context.Context, ev audit.Event) {
	a.events = append(a.events, ev)
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

	// Must use HandleMessageWithEvents with a non-nil handler — nil onEvent
	// now correctly denies immediately (no operator to surface the dialog to).
	noopEvent := func(ChatEvent) {}

	// Start the message handling in a goroutine since it will block on approval.
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessageWithEvents(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-supervised",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		}, noopEvent)
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

	// Must use HandleMessageWithEvents with a non-nil handler — nil onEvent
	// now correctly denies immediately (no operator to surface the dialog to).
	noopEvent := func(ChatEvent) {}

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessageWithEvents(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-denied",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		}, noopEvent)
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

	// Must use HandleMessageWithEvents with a non-nil handler — nil onEvent
	// now correctly denies immediately (no operator to surface the dialog to).
	noopEvent := func(ChatEvent) {}

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessageWithEvents(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-retry",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		}, noopEvent)
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

	// Must use HandleMessageWithEvents with a non-nil handler — nil onEvent
	// now correctly denies immediately (no operator to surface the dialog to).
	// This test verifies timeout behavior when an operator IS present but doesn't respond.
	noopEvent := func(ChatEvent) {}

	err = engine.HandleMessageWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-exhausted",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "search for test",
		Timestamp:  time.Now(),
	}, noopEvent)
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

	// Must use HandleMessageWithEvents with a non-nil handler — nil onEvent
	// now correctly denies immediately (no operator to surface the dialog to).
	noopEvent := func(ChatEvent) {}

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessageWithEvents(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-deny-no-retry",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		}, noopEvent)
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

// ---------------------------------------------------------------------------
// Approval surfacing tests — approvals must never silently time out
// ---------------------------------------------------------------------------

func TestEngine_ApprovalDenied_NilEventHandler(t *testing.T) {
	// When onEvent is nil (no adapter wired), supervised tool calls must be
	// denied immediately with a clear reason — not silently wait for a timeout.
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
	// Set a long timeout to prove we DON'T wait for it.
	engine.SetApprovalConfig(10*time.Second, 0)

	// HandleMessage uses nil onEvent — should deny immediately, not hang.
	start := time.Now()
	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-no-handler",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "search for test",
		Timestamp:  time.Now(),
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	// Must complete much faster than the 10s approval timeout.
	if elapsed > 3*time.Second {
		t.Errorf("HandleMessage took %v — approval was not denied immediately (timeout is 10s)", elapsed)
	}

	// The LLM should have received a denial reason, not a timeout.
	if len(sent.msgs) < 1 {
		t.Fatal("expected at least 1 sent message")
	}

	// No approvals should be left pending — none were submitted.
	pending, _ := mgr.List(context.Background(), approval.StatusPending)
	if len(pending) > 0 {
		t.Errorf("expected 0 pending approvals, got %d", len(pending))
	}
}

func TestEngine_ApprovalEmitted_WithEventHandler(t *testing.T) {
	// When onEvent IS wired, supervised tool calls must emit a tool_approval
	// event so the adapter can render inline buttons for the human operator.
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

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	// Collect events emitted by the engine.
	var events []ChatEvent
	onEvent := func(evt ChatEvent) {
		events = append(events, evt)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessageWithEvents(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-with-handler",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		}, onEvent)
	}()

	// Wait for the approval to be submitted, then approve it.
	time.Sleep(100 * time.Millisecond)
	approvals, _ := mgr.List(ctx, approval.StatusPending)
	if len(approvals) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(approvals))
	}
	_, _ = mgr.Resolve(ctx, approvals[0].ID, true, "test-operator")

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleMessageWithEvents: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for HandleMessageWithEvents")
	}

	// Verify a tool_approval event was emitted with the required fields.
	var approvalEvent *ChatEvent
	for i := range events {
		if events[i].Type == "tool_approval" && events[i].ApprovalID != "" {
			approvalEvent = &events[i]
			break
		}
	}
	if approvalEvent == nil {
		t.Fatal("no tool_approval event was emitted — approval dialog would not reach the operator")
	}
	if approvalEvent.Tool != "web_search" {
		t.Errorf("approval event tool = %q, want web_search", approvalEvent.Tool)
	}
	if approvalEvent.ApprovalCallback == "" {
		t.Error("approval event missing callback data — inline buttons cannot be wired")
	}
	if approvalEvent.Text == "" {
		t.Error("approval event missing summary text")
	}
}

func TestBuildSystemPrompt_IncludesSessionContext(t *testing.T) {
	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", nil, nil, nil, permissions, nil, "Base prompt.", nil, nil, nil, testLogger())

	result := engine.buildSystemPrompt(permissions, adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "387956986",
	})
	if !strings.Contains(result.prompt, "telegram:387956986") {
		t.Error("system prompt should contain the delivery channel")
	}
	if !strings.Contains(result.prompt, "Session Context") {
		t.Error("system prompt should contain Session Context section")
	}
}

func TestBuildSystemPrompt_NoAdapterOmitsContext(t *testing.T) {
	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", nil, nil, nil, permissions, nil, "Base prompt.", nil, nil, nil, testLogger())

	result := engine.buildSystemPrompt(permissions, adapter.IncomingMessage{})
	if strings.Contains(result.prompt, "Session Context") {
		t.Error("system prompt should NOT contain Session Context when adapter is empty")
	}
}

func TestBuildSystemPrompt_IncludesKVGuidanceWhenPersonaSet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("You are helpful."), 0600); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", nil, nil, nil, permissions, p, "", nil, nil, nil, testLogger())

	result := engine.buildSystemPrompt(permissions, adapter.IncomingMessage{})
	if !strings.Contains(result.prompt, "Structured Memory (KV)") {
		t.Error("system prompt should contain Structured Memory (KV) section when persona is set")
	}
	for _, ns := range []string{"`cache:*`", "`log:*`", "`pref:*`", "`state:*`"} {
		if !strings.Contains(result.prompt, ns) {
			t.Errorf("system prompt missing namespace hint %s", ns)
		}
	}
	if !strings.Contains(result.prompt, "Feel free to add new namespaces") {
		t.Error("system prompt should invite the agent to add new namespaces")
	}
}

func TestBuildSystemPrompt_IncludesKVGuidanceOnFallbackPath(t *testing.T) {
	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", nil, nil, nil, permissions, nil, "Base prompt.", nil, nil, nil, testLogger())

	result := engine.buildSystemPrompt(permissions, adapter.IncomingMessage{})
	if !strings.Contains(result.prompt, "Structured Memory (KV)") {
		t.Error("system prompt should contain KV guidance on the fallback path — fallback-path agents have the same kv_* tools wired")
	}
	for _, ns := range []string{"`cache:*`", "`log:*`", "`pref:*`", "`state:*`"} {
		if !strings.Contains(result.prompt, ns) {
			t.Errorf("fallback-path system prompt missing namespace hint %s", ns)
		}
	}
	if !strings.Contains(result.prompt, "Base prompt.") {
		t.Error("fallback prompt content should still be present")
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

func TestEngine_HandleMessage_TruncationNotice(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	mock := &mockProvider{
		response: &llm.ChatResponse{
			Content:    "OK",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	}
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(mock)

	sent := &sentMessages{}
	permissions, _ := security.NewPermissionEngine("supervised")

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "System prompt.", nil, nil, nil, testLogger())
	// Set a very low context limit so we can trigger truncation easily.
	engine.SetMaxContextMessages(5)

	ctx := context.Background()
	convID := "default:test:trunc-chat"

	// Pre-populate 6 messages (exceeds the limit of 5).
	_ = store.GetOrCreateConversationByID(ctx, convID, "test", "trunc-chat")
	for i := 0; i < 6; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		_, _ = store.AddMessage(ctx, convID, StoredMessage{
			Role:    role,
			Content: fmt.Sprintf("msg-%d", i),
		})
	}

	// Send a new message — this brings total to 7, well over the limit of 5.
	msg := adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "trunc-chat",
		UserID:     "user-1",
		Text:       "latest message",
		Timestamp:  time.Now(),
	}
	if err := engine.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	// Verify the LLM received a truncation notice as the second system message.
	req := mock.lastRequest
	if req == nil {
		t.Fatal("mock provider received no request")
	}

	// First message should be the main system prompt.
	if req.Messages[0].Role != "system" {
		t.Fatalf("messages[0].Role = %q, want system", req.Messages[0].Role)
	}
	if !strings.Contains(req.Messages[0].Content, "System prompt.") {
		t.Errorf("messages[0] should contain system prompt, got %q", req.Messages[0].Content)
	}

	// Second message should be the truncation notice.
	if req.Messages[1].Role != "system" {
		t.Fatalf("messages[1].Role = %q, want system (truncation notice)", req.Messages[1].Role)
	}
	if !strings.Contains(req.Messages[1].Content, "truncated") {
		t.Errorf("messages[1] should be truncation notice, got %q", req.Messages[1].Content)
	}

	// The latest user message must be present (not dropped by truncation).
	lastMsg := req.Messages[len(req.Messages)-1]
	if lastMsg.Role != "user" || lastMsg.Content != "latest message" {
		t.Errorf("last message = %s/%q, want user/\"latest message\"", lastMsg.Role, lastMsg.Content)
	}
}

func TestEngine_HandleMessage_NoTruncationNotice_WhenUnderLimit(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	mock := &mockProvider{
		response: &llm.ChatResponse{
			Content:    "OK",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	}
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(mock)

	sent := &sentMessages{}
	permissions, _ := security.NewPermissionEngine("supervised")
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "System prompt.", nil, nil, nil, testLogger())

	ctx := context.Background()
	msg := adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "short-chat",
		UserID:     "user-1",
		Text:       "hello",
		Timestamp:  time.Now(),
	}
	if err := engine.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	// With only 1 message (well under the default limit of 50), there should be
	// no truncation notice — just system prompt + user message.
	req := mock.lastRequest
	if req == nil {
		t.Fatal("mock provider received no request")
	}

	systemCount := 0
	for _, m := range req.Messages {
		if m.Role == "system" {
			systemCount++
		}
	}
	if systemCount != 1 {
		t.Errorf("got %d system messages, want 1 (no truncation notice expected)", systemCount)
	}
}

// --- repeatDetector unit tests ---

func TestRepeatDetector_TriggersOnThreshold(t *testing.T) {
	d := newRepeatDetector(3)
	if d.observe("skill_update", `{"name":"test"}`) {
		t.Fatal("should not trigger on 1st call")
	}
	if d.observe("skill_update", `{"name":"test"}`) {
		t.Fatal("should not trigger on 2nd call")
	}
	if !d.observe("skill_update", `{"name":"test"}`) {
		t.Fatal("should trigger on 3rd consecutive identical call")
	}
}

func TestRepeatDetector_ResetOnDifferentCall(t *testing.T) {
	d := newRepeatDetector(3)
	d.observe("skill_update", `{"name":"test"}`)
	d.observe("skill_update", `{"name":"test"}`)
	// Different tool breaks the streak.
	if d.observe("skill_get", `{"name":"test"}`) {
		t.Fatal("different tool name should reset counter")
	}
	// Same tool but different args also resets.
	d.observe("skill_update", `{"name":"a"}`)
	d.observe("skill_update", `{"name":"a"}`)
	if d.observe("skill_update", `{"name":"b"}`) {
		t.Fatal("different arguments should reset counter")
	}
}

func TestRepeatDetector_AlternatingNeverTriggers(t *testing.T) {
	d := newRepeatDetector(3)
	for i := 0; i < 20; i++ {
		name := "tool_a"
		if i%2 == 1 {
			name = "tool_b"
		}
		if d.observe(name, `{}`) {
			t.Fatalf("alternating calls should never trigger (iteration %d)", i)
		}
	}
}

// --- Engine integration tests for tool loop improvements ---

func TestEngine_ConfigurableMaxToolRounds(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// 4 different tool_calls responses + 1 stop (should never be reached with limit=3).
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls:    []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "tool_a", Arguments: `{"v":1}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				ToolCalls:    []llm.ToolCall{{ID: "c2", Type: "function", Function: llm.FunctionCall{Name: "tool_b", Arguments: `{"v":2}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				ToolCalls:    []llm.ToolCall{{ID: "c3", Type: "function", Function: llm.FunctionCall{Name: "tool_c", Arguments: `{"v":3}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				ToolCalls:    []llm.ToolCall{{ID: "c4", Type: "function", Function: llm.FunctionCall{Name: "tool_d", Arguments: `{"v":4}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "Final.",
				TokensUsed:   llm.TokenUsage{Total: 5},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "Test.", nil, toolMgr, nil, testLogger())
	engine.SetMaxToolRounds(3)

	_, err = engine.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-maxrounds",
		UserID:     "user-1",
		Text:       "Do things",
		Timestamp:  time.Now(),
	}, nil)

	if err == nil {
		t.Fatal("expected error for exceeding max tool rounds")
	}
	if !strings.Contains(err.Error(), "exceeded maximum tool call rounds (3)") {
		t.Errorf("error = %q, want contains 'exceeded maximum tool call rounds (3)'", err.Error())
	}

	// Initial call + 3 tool rounds = 4 provider calls. The 4th response triggers round 3
	// which exceeds the limit check (round >= 3).
	if provider.callIndex != 4 {
		t.Errorf("provider called %d times, want 4", provider.callIndex)
	}
}

func TestEngine_RepeatDetection_IdenticalToolCalls(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// All responses return the same tool call — should trigger repeat detection.
	sameCall := llm.ToolCall{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "skill_update", Arguments: `{"name":"test","content":"x"}`}}
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{sameCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{Content: "Done.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "Test.", nil, toolMgr, nil, testLogger())

	_, err = engine.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-repeat",
		UserID:     "user-1",
		Text:       "Update skill",
		Timestamp:  time.Now(),
	}, nil)

	if err == nil {
		t.Fatal("expected error for repetitive tool calls")
	}
	if !strings.Contains(err.Error(), "identical arguments 3 consecutive times") {
		t.Errorf("error = %q, want contains 'identical arguments 3 consecutive times'", err.Error())
	}
	if !strings.Contains(err.Error(), "skill_update") {
		t.Errorf("error should mention tool name, got: %q", err.Error())
	}

	// Round 0: executes call #1 (consecutiveN=1). Round 1: executes call #2 (consecutiveN=2).
	// Round 2: detects call #3 (consecutiveN=3) before execution → abort.
	// Provider: initial + round 0 completion + round 1 completion = 3 calls.
	if provider.callIndex != 3 {
		t.Errorf("provider called %d times, want 3 (abort before 3rd tool execution)", provider.callIndex)
	}
}

func TestEngine_RepeatDetection_VariedCalls_NoFalsePositive(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls:    []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "skill_get", Arguments: `{"name":"a"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				ToolCalls:    []llm.ToolCall{{ID: "c2", Type: "function", Function: llm.FunctionCall{Name: "skill_update", Arguments: `{"name":"a","content":"new"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				ToolCalls:    []llm.ToolCall{{ID: "c3", Type: "function", Function: llm.FunctionCall{Name: "skill_get", Arguments: `{"name":"b"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "All done.",
				TokensUsed:   llm.TokenUsage{Total: 5},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "Test.", nil, toolMgr, nil, testLogger())

	text, err := engine.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-varied",
		UserID:     "user-1",
		Text:       "Process skills",
		Timestamp:  time.Now(),
	}, nil)

	if err != nil {
		t.Fatalf("varied tool calls should not trigger repeat detection: %v", err)
	}
	if text != "All done." {
		t.Errorf("response = %q, want %q", text, "All done.")
	}
	if provider.callIndex != 4 {
		t.Errorf("provider called %d times, want 4 (all rounds completed)", provider.callIndex)
	}
}

func TestEngine_HandleMessage_EmitsAuditOnLLMError(t *testing.T) {
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

	auditor := &collectingAuditor{}
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, nil, nil, testLogger())
	engine.SetAuditor(auditor)

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

	// Should have at least a session trigger and an LLM error audit event.
	var llmEvents []audit.Event
	for _, ev := range auditor.events {
		if ev.Category == audit.CategoryLLM {
			llmEvents = append(llmEvents, ev)
		}
	}
	if len(llmEvents) == 0 {
		t.Fatal("expected an LLM audit event on error path, got none")
	}
	if llmEvents[0].Status != audit.StatusError {
		t.Errorf("LLM audit status = %q, want %q", llmEvents[0].Status, audit.StatusError)
	}
	if !strings.Contains(llmEvents[0].Detail, "LLM unavailable") {
		t.Errorf("LLM audit detail should contain the error message, got %q", llmEvents[0].Detail)
	}
	// The error event should carry round 0 (pre-loop call).
	if !strings.Contains(llmEvents[0].Detail, `"round":0`) {
		t.Errorf("LLM error audit detail should record round 0, got %q", llmEvents[0].Detail)
	}
}

// TestEngine_HandleMessage_EmitsPerRoundLLMAudit verifies that a multi-round
// tool flow emits one llm.complete audit event per LLM round-trip — round 0
// for the pre-loop call and rounds 1..N for the in-loop calls — rather than a
// single aggregated event at the end.
func TestEngine_HandleMessage_EmitsPerRoundLLMAudit(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				Content:      "thinking about step 1",
				ToolCalls:    []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "skill_get", Arguments: `{"name":"a"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "step 2",
				ToolCalls:    []llm.ToolCall{{ID: "c2", Type: "function", Function: llm.FunctionCall{Name: "skill_get", Arguments: `{"name":"b"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "All done.",
				TokensUsed:   llm.TokenUsage{Total: 5},
				FinishReason: "stop",
			},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	auditor := &collectingAuditor{}
	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "Test.", nil, toolMgr, nil, testLogger())
	engine.SetAuditor(auditor)

	text, err := engine.ChatWithEvents(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-multi",
		UserID:     "user-1",
		Text:       "Process",
		Timestamp:  time.Now(),
	}, nil)
	if err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}
	if text != "All done." {
		t.Errorf("response = %q, want %q", text, "All done.")
	}

	var llmEvents []audit.Event
	for _, ev := range auditor.events {
		if ev.Category == audit.CategoryLLM {
			llmEvents = append(llmEvents, ev)
		}
	}
	// Three LLM round-trips: round 0 (pre-loop) + rounds 1, 2 (in-loop).
	if len(llmEvents) != 3 {
		t.Fatalf("expected 3 LLM audit events, got %d", len(llmEvents))
	}

	expectRoundAndText := []struct {
		round int
		text  string
	}{
		{0, "thinking about step 1"},
		{1, "step 2"},
		{2, "All done."},
	}
	for i, want := range expectRoundAndText {
		ev := llmEvents[i]
		if ev.Status != audit.StatusOK {
			t.Errorf("event[%d] status = %q, want %q", i, ev.Status, audit.StatusOK)
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(ev.Detail), &d); err != nil {
			t.Fatalf("event[%d] detail unmarshal: %v", i, err)
		}
		gotRound, ok := d["round"].(float64)
		if !ok || int(gotRound) != want.round {
			t.Errorf("event[%d] round = %v, want %d", i, d["round"], want.round)
		}
		if got, _ := d["response_text"].(string); got != want.text {
			t.Errorf("event[%d] response_text = %q, want %q", i, got, want.text)
		}
	}
}

func TestBuildTriggerAuditDetail_UserMessage(t *testing.T) {
	msg := adapter.IncomingMessage{
		Adapter:  "telegram",
		UserID:   "u123",
		UserName: "alice",
		Text:     "hello",
	}
	d := buildTriggerAuditDetail(msg)
	if d["trigger_type"] != "user" {
		t.Errorf("trigger_type = %q, want user", d["trigger_type"])
	}
	if d["adapter"] != "telegram" {
		t.Errorf("adapter = %q, want telegram", d["adapter"])
	}
	if _, ok := d["skill_name"]; ok {
		t.Error("skill_name should be absent for user messages")
	}
	if _, ok := d["schedule_name"]; ok {
		t.Error("schedule_name should be absent for user messages")
	}
}

func TestBuildTriggerAuditDetail_ScheduledMessage(t *testing.T) {
	msg := adapter.IncomingMessage{
		Adapter:      "telegram",
		UserName:     "scheduler",
		Text:         "[Scheduled: heartbeat]",
		IsScheduled:  true,
		SkillName:    "heartbeat",
		ScheduleName: "heartbeat-hourly",
		ScheduleCron: "0 * * * *",
	}
	d := buildTriggerAuditDetail(msg)
	if d["trigger_type"] != "schedule" {
		t.Errorf("trigger_type = %q, want schedule", d["trigger_type"])
	}
	if d["skill_name"] != "heartbeat" {
		t.Errorf("skill_name = %q, want heartbeat", d["skill_name"])
	}
	if d["schedule_name"] != "heartbeat-hourly" {
		t.Errorf("schedule_name = %q, want heartbeat-hourly", d["schedule_name"])
	}
	if d["schedule_cron"] != "0 * * * *" {
		t.Errorf("schedule_cron = %q, want 0 * * * *", d["schedule_cron"])
	}
}

func TestEngine_AuditSource_UserMessage(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      "Hi!",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	sent := &sentMessages{}
	perms, _ := security.NewPermissionEngine("autonomous")
	auditor := &collectingAuditor{}
	engine := NewEngine("default", router, store, sent.send, perms, nil, "Test.", nil, nil, nil, testLogger())
	engine.SetAuditor(auditor)

	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "chat-1",
		UserName:   "alice",
		Text:       "hello",
		Timestamp:  time.Now(),
	}
	if err := engine.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var triggerEv *audit.Event
	for i := range auditor.events {
		if auditor.events[i].Category == audit.CategorySession && auditor.events[i].Action == "trigger" {
			triggerEv = &auditor.events[i]
			break
		}
	}
	if triggerEv == nil {
		t.Fatal("no session trigger audit event emitted")
	}
	if triggerEv.Source != "telegram" {
		t.Errorf("Source = %q, want telegram", triggerEv.Source)
	}
}

func TestEngine_AuditSource_ScheduledMessage(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      "Heartbeat OK",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	sent := &sentMessages{}
	perms, _ := security.NewPermissionEngine("autonomous")
	auditor := &collectingAuditor{}
	engine := NewEngine("default", router, store, sent.send, perms, nil, "Test.", nil, nil, nil, testLogger())
	engine.SetAuditor(auditor)

	msg := adapter.IncomingMessage{
		Adapter:      "telegram",
		ExternalID:   "chat-1",
		UserName:     "scheduler",
		Text:         "[Scheduled: heartbeat]",
		IsScheduled:  true,
		SkillName:    "heartbeat",
		ScheduleName: "heartbeat-hourly",
		ScheduleCron: "0 * * * *",
		Timestamp:    time.Now(),
	}
	if err := engine.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var triggerEv *audit.Event
	for i := range auditor.events {
		if auditor.events[i].Category == audit.CategorySession && auditor.events[i].Action == "trigger" {
			triggerEv = &auditor.events[i]
			break
		}
	}
	if triggerEv == nil {
		t.Fatal("no session trigger audit event emitted")
	}
	if triggerEv.Source != "scheduler" {
		t.Errorf("Source = %q, want scheduler", triggerEv.Source)
	}
}

// ---------------------------------------------------------------------------
// Supervisor agent tests
// ---------------------------------------------------------------------------

func TestParseSupervisorResponse_Approve(t *testing.T) {
	decision, reason := parseSupervisorResponse("APPROVE: tool call aligns with user request")
	if decision != supervisorApprove {
		t.Errorf("decision = %q, want APPROVE", decision)
	}
	if reason != "tool call aligns with user request" {
		t.Errorf("reason = %q, want 'tool call aligns with user request'", reason)
	}
}

func TestParseSupervisorResponse_Deny(t *testing.T) {
	decision, reason := parseSupervisorResponse("DENY: arguments contain potential injection")
	if decision != supervisorDeny {
		t.Errorf("decision = %q, want DENY", decision)
	}
	if reason != "arguments contain potential injection" {
		t.Errorf("reason = %q", reason)
	}
}

func TestParseSupervisorResponse_Escalate(t *testing.T) {
	decision, reason := parseSupervisorResponse("ESCALATE: unusual tool usage pattern, human review recommended")
	if decision != supervisorEscalate {
		t.Errorf("decision = %q, want ESCALATE", decision)
	}
	if reason != "unusual tool usage pattern, human review recommended" {
		t.Errorf("reason = %q", reason)
	}
}

func TestParseSupervisorResponse_WithLeadingText(t *testing.T) {
	// Some LLMs add text before the decision line.
	resp := "Based on my analysis:\n\nAPPROVE: safe operation"
	decision, reason := parseSupervisorResponse(resp)
	if decision != supervisorApprove {
		t.Errorf("decision = %q, want APPROVE", decision)
	}
	if reason != "safe operation" {
		t.Errorf("reason = %q", reason)
	}
}

func TestParseSupervisorResponse_Unparseable(t *testing.T) {
	decision, reason := parseSupervisorResponse("I think this tool call is fine.")
	if decision != supervisorEscalate {
		t.Errorf("decision = %q, want ESCALATE (fallback)", decision)
	}
	if !strings.Contains(reason, "could not parse") {
		t.Errorf("reason = %q, want to contain 'could not parse'", reason)
	}
}

func TestParseSupervisorResponse_CaseInsensitive(t *testing.T) {
	decision, _ := parseSupervisorResponse("approve: looks good")
	if decision != supervisorApprove {
		t.Errorf("decision = %q, want APPROVE (case insensitive)", decision)
	}
}

func TestEngine_SupervisorApprove(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Primary agent's provider: tool call → final response.
	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "Here are the results.",
				TokensUsed:   llm.TokenUsage{Total: 20},
				FinishReason: "stop",
			},
		},
	}

	// Supervisor's provider: returns APPROVE.
	supervisorProv := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "APPROVE: tool call aligns with user request", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)

	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	// Create supervisor engine (no tools, autonomous).
	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var events []ChatEvent
	_, chatErr := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-sup-approve",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "search for test",
		Timestamp:  time.Now(),
	}, func(evt ChatEvent) { events = append(events, evt) })
	if chatErr != nil {
		t.Fatalf("ChatWithEvents: %v", chatErr)
	}

	// Verify supervisor_approved event was emitted.
	found := false
	for _, evt := range events {
		if evt.Type == "tool_approval" && evt.ApprovalStatus == "supervisor_approved" {
			found = true
			if !strings.Contains(evt.Text, "Approved by supervisor") {
				t.Errorf("approval text = %q, want to contain 'Approved by supervisor'", evt.Text)
			}
		}
	}
	if !found {
		t.Error("no supervisor_approved event found")
	}
}

func TestEngine_SupervisorDeny(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Primary agent: tool call → LLM gets denial → final response.
	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "dangerous_tool", Arguments: `{"cmd":"rm -rf /"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				Content:      "I understand that tool call was denied.",
				TokensUsed:   llm.TokenUsage{Total: 15},
				FinishReason: "stop",
			},
		},
	}

	// Supervisor: returns DENY.
	supervisorProv := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "DENY: dangerous operation — potential system destruction", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)

	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var events []ChatEvent
	_, chatErr := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-sup-deny",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "delete everything",
		Timestamp:  time.Now(),
	}, func(evt ChatEvent) { events = append(events, evt) })
	if chatErr != nil {
		t.Fatalf("ChatWithEvents: %v", chatErr)
	}

	// Verify supervisor_denied event was emitted.
	found := false
	for _, evt := range events {
		if evt.Type == "tool_approval" && evt.ApprovalStatus == "supervisor_denied" {
			found = true
			if !strings.Contains(evt.Text, "Denied by supervisor") {
				t.Errorf("approval text = %q, want to contain 'Denied by supervisor'", evt.Text)
			}
		}
	}
	if !found {
		t.Error("no supervisor_denied event found")
	}
}

func TestEngine_SupervisorDeny_RepeatAutoDenied(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Primary agent retries the identical denied call in round 2; the Engine
	// must auto-deny it without a second supervisor review.
	identicalCall := llm.ToolCall{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "dangerous_tool", Arguments: `{"cmd":"rm -rf /"}`}}
	retryCall := identicalCall
	retryCall.ID = "call_2"
	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{identicalCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{retryCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{Content: "Understood, I will not retry.", TokensUsed: llm.TokenUsage{Total: 15}, FinishReason: "stop"},
		},
	}

	// Supervisor has exactly ONE response: a second review attempt would fail
	// with "no more mock responses" and surface as supervisor_error.
	supervisorProv := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "DENY: dangerous operation", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)

	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var events []ChatEvent
	_, chatErr := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-sup-deny-repeat",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "delete everything",
		Timestamp:  time.Now(),
	}, func(evt ChatEvent) { events = append(events, evt) })
	if chatErr != nil {
		t.Fatalf("ChatWithEvents: %v", chatErr)
	}

	if supervisorProv.callIndex != 1 {
		t.Errorf("supervisor LLM called %d times, want 1", supervisorProv.callIndex)
	}
	var supervisorDenied, autoDenied int
	for _, evt := range events {
		if evt.Type != "tool_approval" {
			continue
		}
		switch evt.ApprovalStatus {
		case "supervisor_denied":
			supervisorDenied++
		case "auto_denied":
			autoDenied++
		case "supervisor_error":
			t.Errorf("unexpected supervisor_error event: %q", evt.Text)
		}
	}
	if supervisorDenied != 1 {
		t.Errorf("supervisor_denied events = %d, want 1", supervisorDenied)
	}
	if autoDenied != 1 {
		t.Errorf("auto_denied events = %d, want 1", autoDenied)
	}
}

func TestEngine_SupervisorDeny_DifferentArgsReviewedAgain(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Same tool, different arguments: dedup must NOT trigger and the
	// supervisor must review the second call.
	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls:    []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "dangerous_tool", Arguments: `{"cmd":"rm -rf /"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{
				ToolCalls:    []llm.ToolCall{{ID: "call_2", Type: "function", Function: llm.FunctionCall{Name: "dangerous_tool", Arguments: `{"cmd":"rm -rf /tmp"}`}}},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{Content: "Both denied, giving up.", TokensUsed: llm.TokenUsage{Total: 15}, FinishReason: "stop"},
		},
	}

	supervisorProv := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: "DENY: dangerous operation", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
			{Content: "DENY: still dangerous", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)

	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var events []ChatEvent
	_, chatErr := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-sup-deny-diffargs",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "delete everything",
		Timestamp:  time.Now(),
	}, func(evt ChatEvent) { events = append(events, evt) })
	if chatErr != nil {
		t.Fatalf("ChatWithEvents: %v", chatErr)
	}

	if supervisorProv.callIndex != 2 {
		t.Errorf("supervisor LLM called %d times, want 2", supervisorProv.callIndex)
	}
	for _, evt := range events {
		if evt.Type == "tool_approval" && evt.ApprovalStatus == "auto_denied" {
			t.Errorf("unexpected auto_denied event for different arguments: %q", evt.Text)
		}
	}
}

func TestEngine_HumanDeny_RepeatAutoDenied(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	identicalCall := llm.ToolCall{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}}
	retryCall := identicalCall
	retryCall.ID = "call_2"
	provider := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{identicalCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{ToolCalls: []llm.ToolCall{retryCall}, TokensUsed: llm.TokenUsage{Total: 10}, FinishReason: "tool_calls"},
			{Content: "Understood, I will not retry.", TokensUsed: llm.TokenUsage{Total: 15}, FinishReason: "stop"},
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
	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, sent.send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	var events []ChatEvent
	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.HandleMessageWithEvents(ctx, adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "chat-human-deny-repeat",
			UserID:     "user-1",
			UserName:   "testuser",
			Text:       "search for test",
			Timestamp:  time.Now(),
		}, func(evt ChatEvent) {
			mu.Lock()
			events = append(events, evt)
			mu.Unlock()
		})
	}()

	// Deny the first (and only) approval request. The identical retry must be
	// auto-denied without a second pending approval — if dedup fails, the run
	// blocks on an unresolved approval and the context times out.
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
		t.Fatal("timed out waiting for HandleMessage — identical retry likely submitted a second approval")
	}

	mu.Lock()
	defer mu.Unlock()
	autoDenied := 0
	for _, evt := range events {
		if evt.Type == "tool_approval" && evt.ApprovalStatus == "auto_denied" {
			autoDenied++
		}
	}
	if autoDenied != 1 {
		t.Errorf("auto_denied events = %d, want 1", autoDenied)
	}
}

func TestEngine_SupervisorAuditDetailIncludesRawResponse(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"x"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{Content: "done", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	rawSupervisor := "Reasoning: the query looks benign and aligns with the user request.\nAPPROVE: safe search query"
	supervisorProv := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{Content: rawSupervisor, TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)
	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "", nil, toolMgr, mgr, testLogger())
	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	auditor := &collectingAuditor{}
	engine.SetAuditor(auditor)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter: "test", ExternalID: "chat-sup-raw", UserID: "u", UserName: "t",
		Text: "search", Timestamp: time.Now(),
	}, nil); err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}

	var supEv *audit.Event
	for i := range auditor.events {
		if auditor.events[i].Category == audit.CategorySupervisor {
			supEv = &auditor.events[i]
			break
		}
	}
	if supEv == nil {
		t.Fatal("no supervisor audit event emitted")
	}

	var detail map[string]any
	if err := json.Unmarshal([]byte(supEv.Detail), &detail); err != nil {
		t.Fatalf("Detail not JSON: %v", err)
	}
	got, ok := detail["raw_response"].(string)
	if !ok {
		t.Fatalf("raw_response missing or not string: %#v", detail["raw_response"])
	}
	if got != rawSupervisor {
		t.Errorf("raw_response = %q, want %q", got, rawSupervisor)
	}
	if detail["decision"] != "APPROVE" {
		t.Errorf("decision = %v, want APPROVE", detail["decision"])
	}
}

func TestEngine_SetSupervisor(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)

	engine := NewEngine("default", router, store, nil, nil, nil, "", nil, nil, nil, testLogger())

	if engine.Supervisor() != nil {
		t.Error("expected nil supervisor initially")
	}

	supEngine := NewEngine("supervisor", router, store, nil, nil, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	if engine.Supervisor() != supEngine {
		t.Error("expected supervisor to be set")
	}

	engine.SetSupervisor(nil)
	if engine.Supervisor() != nil {
		t.Error("expected supervisor to be cleared")
	}
}

// TestEngine_SupervisorError verifies the silent-failure fix: when the
// supervisor's LLM call errors out, an audit event is emitted (so failures
// are visible) and a supervisor_error ChatEvent is fired (so chat UIs can
// show why a tool dropped to human approval).
func TestEngine_SupervisorError(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"x"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{Content: "done", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	// Supervisor provider has zero responses → ChatCompletion returns an error
	// immediately, simulating an LLM/network failure.
	supervisorProv := &sequentialProvider{}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)
	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "", nil, toolMgr, mgr, testLogger())
	// Short approval timeout so the human-approval fall-through doesn't block
	// the test waiting 5m for an operator that will never respond.
	engine.SetApprovalConfig(50*time.Millisecond, 0)

	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	auditor := &collectingAuditor{}
	engine.SetAuditor(auditor)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var events []ChatEvent
	if _, err := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter: "test", ExternalID: "chat-sup-err", UserID: "u", UserName: "t",
		Text: "search", Timestamp: time.Now(),
	}, func(evt ChatEvent) { events = append(events, evt) }); err != nil {
		t.Fatalf("ChatWithEvents: %v", err)
	}

	// Verify supervisor_error ChatEvent was emitted.
	var sawSupErrEvent bool
	for _, evt := range events {
		if evt.Type == "tool_approval" && evt.ApprovalStatus == "supervisor_error" {
			sawSupErrEvent = true
			if !strings.Contains(evt.Text, "Supervisor unavailable") {
				t.Errorf("supervisor_error event text = %q, want to contain 'Supervisor unavailable'", evt.Text)
			}
			break
		}
	}
	if !sawSupErrEvent {
		t.Error("no supervisor_error tool_approval event found")
	}

	// Verify a supervisor audit event with status=error was emitted.
	var supErrAudit *audit.Event
	for i := range auditor.events {
		ev := &auditor.events[i]
		if ev.Category == audit.CategorySupervisor && ev.Status == audit.StatusError {
			supErrAudit = ev
			break
		}
	}
	if supErrAudit == nil {
		t.Fatal("no supervisor audit event with status=error emitted")
	}
	if supErrAudit.Source != "supervisor:supervisor" {
		t.Errorf("audit Source = %q, want %q", supErrAudit.Source, "supervisor:supervisor")
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(supErrAudit.Detail), &detail); err != nil {
		t.Fatalf("audit Detail not JSON: %v", err)
	}
	if detail["decision"] != "error" {
		t.Errorf("audit detail decision = %v, want \"error\"", detail["decision"])
	}
	if detail["tool"] != "web_search" {
		t.Errorf("audit detail tool = %v, want \"web_search\"", detail["tool"])
	}
	if reason, _ := detail["reason"].(string); reason == "" {
		t.Error("audit detail reason is empty; want the underlying error message")
	}
}

// capturingProvider records all ChatRequests it receives and returns
// pre-configured responses sequentially (same as sequentialProvider).
type capturingProvider struct {
	responses []*llm.ChatResponse
	callIndex int
	requests  []llm.ChatRequest
}

func (c *capturingProvider) Name() string { return "mock" }
func (c *capturingProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	c.requests = append(c.requests, req)
	if c.callIndex >= len(c.responses) {
		return nil, fmt.Errorf("no more mock responses (call %d)", c.callIndex)
	}
	resp := c.responses[c.callIndex]
	c.callIndex++
	return resp, nil
}
func (c *capturingProvider) HealthCheck(_ context.Context) error { return nil }

func TestEngine_SupervisorReview_ScheduledSkillContext(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "kv_delete", Arguments: `{"key":"cache:old"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{Content: "Done.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	supervisorProv := &capturingProvider{
		responses: []*llm.ChatResponse{
			{Content: "APPROVE: aligns with heartbeat skill purpose", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)
	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	// Load a skill so it can be matched by SkillName.
	engine.AppendSkill(skill.Skill{
		Name:        "heartbeat",
		Description: "Periodic health check and cache cleanup.\nAlso updates status logs in KV store.",
		Body:        "This is your periodic check-in.\n\n1. Check your Pamela project — review My TODOs and act on anything actionable.",
		Triggers:    []string{"schedule:heartbeat"},
		ParsedTriggers: []skill.Trigger{
			{Type: skill.TriggerSchedule, Raw: "schedule:heartbeat"},
		},
	})

	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, chatErr := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:      "test",
		ExternalID:   "chat-sup-skill",
		UserID:       "scheduler",
		UserName:     "scheduler",
		Text:         "[Scheduled: heartbeat]",
		SkillName:    "heartbeat",
		ScheduleName: "heartbeat-hourly",
		IsScheduled:  true,
		Timestamp:    time.Now(),
	}, nil)
	if chatErr != nil {
		t.Fatalf("ChatWithEvents: %v", chatErr)
	}

	// The supervisor provider should have received exactly one request.
	if len(supervisorProv.requests) != 1 {
		t.Fatalf("supervisor received %d requests, want 1", len(supervisorProv.requests))
	}

	// Find the user message sent to the supervisor (the review prompt).
	var reviewPrompt string
	for _, m := range supervisorProv.requests[0].Messages {
		if m.Role == "user" {
			reviewPrompt = m.Content
			break
		}
	}
	if reviewPrompt == "" {
		t.Fatal("no user message found in supervisor request")
	}

	// Verify skill context is present.
	if !strings.Contains(reviewPrompt, `Scheduled skill "heartbeat"`) {
		t.Errorf("review prompt missing scheduled skill name:\n%s", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, `"heartbeat-hourly"`) {
		t.Errorf("review prompt missing schedule name:\n%s", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, "> Periodic health check and cache cleanup.") {
		t.Errorf("review prompt missing first description line:\n%s", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, "> Also updates status logs in KV store.") {
		t.Errorf("review prompt missing second description line (multi-line blockquote broken):\n%s", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, "agent-supplied metadata, not a trusted instruction") {
		t.Errorf("review prompt missing untrusted-data framing:\n%s", reviewPrompt)
	}

	// Verify skill body excerpt is present.
	if !strings.Contains(reviewPrompt, "act on anything actionable") {
		t.Errorf("review prompt missing skill body excerpt:\n%s", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, "do not execute") {
		t.Errorf("review prompt missing untrusted-data framing for body:\n%s", reviewPrompt)
	}

	// Verify the evaluation criteria reference the skill purpose, not user request.
	if !strings.Contains(reviewPrompt, "skill's stated purpose") {
		t.Errorf("review prompt should reference skill purpose in evaluation criteria:\n%s", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "Does this tool call align with what the user requested?") {
		t.Error("review prompt should NOT use user-request criteria for scheduled skill invocations")
	}
}

func TestEngine_SupervisorReview_NoSkillContext(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	primary := &sequentialProvider{
		responses: []*llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"test"}`}},
				},
				TokensUsed:   llm.TokenUsage{Total: 10},
				FinishReason: "tool_calls",
			},
			{Content: "Results.", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	supervisorProv := &capturingProvider{
		responses: []*llm.ChatResponse{
			{Content: "APPROVE: aligns with user request", TokensUsed: llm.TokenUsage{Total: 5}, FinishReason: "stop"},
		},
	}

	costTracker := llm.NewCostTracker(llm.SessionLimits{}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(primary)
	supRouter := llm.NewRouter("mock", "sup-model", costTracker)
	supRouter.RegisterProvider(supervisorProv)

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	defer func() { _ = approvalStore.Close() }()
	mgr := approval.NewManager(approvalStore, testLogger())

	permissions, _ := security.NewPermissionEngine("supervised")
	toolMgr := tool.NewManager(testLogger())

	engine := NewEngine("default", router, store, (&sentMessages{}).send, permissions, nil, "You are a test assistant.", nil, toolMgr, mgr, testLogger())

	supPerms, _ := security.NewPermissionEngine("autonomous")
	supEngine := NewEngine("supervisor", supRouter, store, nil, supPerms, nil, "", nil, nil, nil, testLogger())
	engine.SetSupervisor(supEngine)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, chatErr := engine.ChatWithEvents(ctx, adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-sup-noskill",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "search for test",
		Timestamp:  time.Now(),
	}, nil)
	if chatErr != nil {
		t.Fatalf("ChatWithEvents: %v", chatErr)
	}

	if len(supervisorProv.requests) != 1 {
		t.Fatalf("supervisor received %d requests, want 1", len(supervisorProv.requests))
	}

	var reviewPrompt string
	for _, m := range supervisorProv.requests[0].Messages {
		if m.Role == "user" {
			reviewPrompt = m.Content
			break
		}
	}

	// Verify no skill context lines appear.
	if strings.Contains(reviewPrompt, "Invocation") {
		t.Errorf("review prompt should not contain skill invocation for regular messages:\n%s", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "Skill purpose") {
		t.Errorf("review prompt should not contain skill purpose for regular messages:\n%s", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "Skill instructions") {
		t.Errorf("review prompt should not contain skill body for regular messages:\n%s", reviewPrompt)
	}

	// Verify the original evaluation criteria are used.
	if !strings.Contains(reviewPrompt, "Does this tool call align with what the user requested?") {
		t.Errorf("review prompt should use user-request criteria for regular messages:\n%s", reviewPrompt)
	}
}

// --------------------------------------------------------------------------
// Tests: nudge counters
// --------------------------------------------------------------------------

func newNudgeEngine(t *testing.T, memInterval, skillInterval int) *Engine {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      "ok",
			TokensUsed:   llm.TokenUsage{Prompt: 5, Completion: 5, Total: 10},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	perms, _ := security.NewPermissionEngine("autonomous")
	e := NewEngine("test", router, store, func(_ context.Context, _ adapter.OutgoingMessage) error { return nil }, perms, nil, "test", nil, nil, nil, testLogger())
	e.SetNudgeConfig(memInterval, skillInterval)
	return e
}

func TestNudge_MemoryFiresAtThreshold(t *testing.T) {
	e := newNudgeEngine(t, 3, 0)
	convID := "conv-1"

	for i := 0; i < 2; i++ {
		e.nudgeIncTurns(convID)
	}
	mem, _ := e.nudgeShouldReview(convID)
	if mem {
		t.Error("should not trigger review before threshold")
	}

	e.nudgeIncTurns(convID)
	mem, _ = e.nudgeShouldReview(convID)
	if !mem {
		t.Error("should trigger memory review at threshold")
	}
}

func TestNudge_SkillFiresOnHighToolUse(t *testing.T) {
	e := newNudgeEngine(t, 0, 5)
	convID := "conv-1"

	e.nudgeIncToolRounds(convID, 4)
	_, sk := e.nudgeShouldReview(convID)
	if sk {
		t.Error("should not trigger review before threshold")
	}

	e.nudgeIncToolRounds(convID, 1)
	_, sk = e.nudgeShouldReview(convID)
	if !sk {
		t.Error("should trigger skill review at threshold")
	}
}

func TestNudge_AgentSelfWriteResets(t *testing.T) {
	e := newNudgeEngine(t, 3, 5)
	convID := "conv-1"

	e.nudgeIncTurns(convID)
	e.nudgeIncTurns(convID)
	e.nudgeIncToolRounds(convID, 4)

	e.NudgeResetExternal(convID, "memory")
	mem, sk := e.nudgeShouldReview(convID)
	if mem {
		t.Error("memory counter should be reset")
	}
	if sk {
		t.Error("skill counter should still be below threshold")
	}

	e.nudgeIncTurns(convID)
	e.nudgeIncTurns(convID)
	e.nudgeIncTurns(convID)
	mem, _ = e.nudgeShouldReview(convID)
	if !mem {
		t.Error("memory should fire again after reset + 3 more turns")
	}
}

func TestNudge_IsolatedAcrossConversations(t *testing.T) {
	e := newNudgeEngine(t, 2, 0)

	e.nudgeIncTurns("conv-a")
	e.nudgeIncTurns("conv-a")

	e.nudgeIncTurns("conv-b")

	memA, _ := e.nudgeShouldReview("conv-a")
	memB, _ := e.nudgeShouldReview("conv-b")

	if !memA {
		t.Error("conv-a should trigger at 2 turns")
	}
	if memB {
		t.Error("conv-b should NOT trigger at 1 turn")
	}
}

func TestNudge_ZeroIntervalNeverFires(t *testing.T) {
	e := newNudgeEngine(t, 0, 0)
	convID := "conv-1"

	for i := 0; i < 100; i++ {
		e.nudgeIncTurns(convID)
		e.nudgeIncToolRounds(convID, 10)
	}
	mem, sk := e.nudgeShouldReview(convID)
	if mem || sk {
		t.Error("should never trigger with zero intervals")
	}
}

// --------------------------------------------------------------------------
// Tests: review lifecycle
// --------------------------------------------------------------------------

func TestReview_NoOpWhenNoReviewer(t *testing.T) {
	e := newNudgeEngine(t, 1, 0)
	// No reviewer set — maybeRunReview should be a no-op
	e.maybeRunReview("conv-1", true, true)
	// Success = no panic, no goroutine leak
}

func TestReview_NoOpBelowThreshold(t *testing.T) {
	e := newNudgeEngine(t, 5, 10)
	convID := "conv-1"

	e.nudgeIncTurns(convID)
	e.nudgeIncToolRounds(convID, 2)

	mem, sk := e.nudgeShouldReview(convID)
	if mem || sk {
		t.Error("should not trigger below both thresholds")
	}
}

func TestReview_BuildReviewPrompt_BothFlags(t *testing.T) {
	prompt := buildReviewPrompt(true, true)
	if !strings.Contains(prompt, "MEMORY") {
		t.Error("should contain memory review prompt")
	}
	if !strings.Contains(prompt, "skill") {
		t.Error("should contain skill review prompt")
	}
}

func TestReview_BuildReviewPrompt_MemoryOnly(t *testing.T) {
	prompt := buildReviewPrompt(true, false)
	if !strings.Contains(prompt, "MEMORY") {
		t.Error("should contain memory review prompt")
	}
	if strings.Contains(prompt, "skills should be created") {
		t.Error("should NOT contain skill review prompt")
	}
}

func TestReview_BuildReviewPrompt_NeitherFlag(t *testing.T) {
	prompt := buildReviewPrompt(false, false)
	if prompt != "" {
		t.Errorf("expected empty prompt, got %q", prompt)
	}
}

func TestReview_NudgeResetAfterReview(t *testing.T) {
	e := newNudgeEngine(t, 2, 3)
	convID := "conv-1"

	e.nudgeIncTurns(convID)
	e.nudgeIncTurns(convID)
	e.nudgeIncToolRounds(convID, 3)

	mem, sk := e.nudgeShouldReview(convID)
	if !mem || !sk {
		t.Fatal("should trigger both reviews")
	}

	e.nudgeReset(convID, true, true)
	mem, sk = e.nudgeShouldReview(convID)
	if mem || sk {
		t.Error("should not trigger after reset")
	}
}

func TestReview_DoesNotBlockResponse(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      "Hello!",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})
	perms, _ := security.NewPermissionEngine("autonomous")

	main := NewEngine("main", router, store, func(_ context.Context, _ adapter.OutgoingMessage) error { return nil }, perms, nil, "test", nil, nil, nil, testLogger())
	main.SetNudgeConfig(1, 0) // trigger every turn

	reviewerRouter := llm.NewRouter("mock", "test-model", costTracker)
	reviewerRouter.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      "Memory is up to date.",
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	reviewStore, _ := NewInMemoryStore()
	defer func() { _ = reviewStore.Close() }()
	revPerms, _ := security.NewPermissionEngine("autonomous")
	reviewer := NewEngine("reviewer", reviewerRouter, reviewStore, func(_ context.Context, _ adapter.OutgoingMessage) error { return nil }, revPerms, nil, "test", nil, nil, nil, testLogger())
	main.SetReviewer(reviewer)

	// HandleMessage should return without waiting for reviewer
	done := make(chan error, 1)
	go func() {
		done <- main.HandleMessage(context.Background(), adapter.IncomingMessage{
			Adapter:    "test",
			ExternalID: "test-1",
			Text:       "Hi",
			Timestamp:  time.Now(),
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("HandleMessage: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HandleMessage blocked — reviewer may be blocking the response")
	}
}

func TestReview_TimeoutBounded(t *testing.T) {
	e := newNudgeEngine(t, 0, 0)
	e.SetReviewerConfig(0, 100*time.Millisecond) // very short timeout

	store, _ := NewInMemoryStore()
	defer func() { _ = store.Close() }()
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	slowRouter := llm.NewRouter("mock", "test-model", costTracker)
	slowRouter.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:      "done",
			TokensUsed:   llm.TokenUsage{Total: 10},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})
	revPerms, _ := security.NewPermissionEngine("autonomous")
	reviewer := NewEngine("reviewer", slowRouter, store, func(_ context.Context, _ adapter.OutgoingMessage) error { return nil }, revPerms, nil, "test", nil, nil, nil, testLogger())
	e.SetReviewer(reviewer)

	// Should not panic; timeout will limit duration
	e.maybeRunReview("conv-1", true, false)
	time.Sleep(200 * time.Millisecond) // let goroutine finish
}
