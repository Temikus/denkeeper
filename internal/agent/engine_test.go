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
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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
	if mp.lastRequest.Messages[0].Content != customPrompt {
		t.Errorf("system prompt = %q, want %q", mp.lastRequest.Messages[0].Content, customPrompt)
	}
}

func TestExtractMemoryUpdate_Present(t *testing.T) {
	text := "Here is my answer.\n\n[MEMORY_UPDATE]\nUser prefers concise answers.\n[/MEMORY_UPDATE]"
	cleaned, update := extractMemoryUpdate(text)
	if cleaned != "Here is my answer." {
		t.Errorf("cleaned = %q, want %q", cleaned, "Here is my answer.")
	}
	if update != "User prefers concise answers." {
		t.Errorf("update = %q, want %q", update, "User prefers concise answers.")
	}
}

func TestExtractMemoryUpdate_Absent(t *testing.T) {
	text := "Just a normal response."
	cleaned, update := extractMemoryUpdate(text)
	if cleaned != text {
		t.Errorf("cleaned = %q, want original text", cleaned)
	}
	if update != "" {
		t.Errorf("update = %q, want empty", update)
	}
}

func TestExtractMemoryUpdate_MissingCloseTag(t *testing.T) {
	text := "Answer.\n\n[MEMORY_UPDATE]\nSome content without close tag."
	cleaned, update := extractMemoryUpdate(text)
	if cleaned != text {
		t.Errorf("cleaned should be unchanged when close tag is missing")
	}
	if update != "" {
		t.Errorf("update = %q, want empty", update)
	}
}

func TestExtractMemoryUpdate_InMiddle(t *testing.T) {
	text := "Before.\n\n[MEMORY_UPDATE]\nMemory content.\n[/MEMORY_UPDATE]\n\nAfter."
	cleaned, update := extractMemoryUpdate(text)
	if !strings.Contains(cleaned, "Before.") || !strings.Contains(cleaned, "After.") {
		t.Errorf("cleaned = %q, want it to contain Before. and After.", cleaned)
	}
	if strings.Contains(cleaned, "MEMORY_UPDATE") {
		t.Error("cleaned should not contain memory update tags")
	}
	if update != "Memory content." {
		t.Errorf("update = %q, want %q", update, "Memory content.")
	}
}

func TestEngine_HandleMessage_MemoryUpdate(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Create a persona dir with a SOUL.md so we have a writable persona.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Hello!\n\n[MEMORY_UPDATE]\nUser said hi.\n[/MEMORY_UPDATE]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, sent.send, permissions, p, "", nil, nil, nil, testLogger())

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hi",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	// The sent message should have the memory directive stripped.
	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
	if sent.msgs[0].Text != "Hello!" {
		t.Errorf("sent text = %q, want %q", sent.msgs[0].Text, "Hello!")
	}

	// The stored message should also be stripped.
	msgs, err := store.GetMessages(context.Background(), "default:test:chat-1", 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("stored %d messages, want >= 2", len(msgs))
	}
	if msgs[1].Content != "Hello!" {
		t.Errorf("stored assistant content = %q, want %q", msgs[1].Content, "Hello!")
	}

	// The persona's in-memory state should be updated.
	if p.Memory != "User said hi." {
		t.Errorf("persona.Memory = %q, want %q", p.Memory, "User said hi.")
	}

	// The MEMORY.md file should have been written.
	data, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("reading MEMORY.md: %v", err)
	}
	if strings.TrimSpace(string(data)) != "User said hi." {
		t.Errorf("MEMORY.md = %q, want %q", strings.TrimSpace(string(data)), "User said hi.")
	}
}

func TestEngine_HandleMessage_NoMemoryUpdateWithoutPersona(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Hello!\n\n[MEMORY_UPDATE]\nShould not persist.\n[/MEMORY_UPDATE]",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	sent := &sentMessages{}
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// No persona — memory update should be stripped but not persisted.
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

	// Directive should still be stripped from the user-facing message.
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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

	costTracker := llm.NewCostTracker(10.0)
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
// USER_UPDATE directive tests
// ---------------------------------------------------------------------------

func TestExtractUserUpdate_Found(t *testing.T) {
	text := "Here is my answer.\n\n[USER_UPDATE]\nUser prefers brief answers.\n[/USER_UPDATE]"
	cleaned, update := extractUserUpdate(text)
	if cleaned != "Here is my answer." {
		t.Errorf("cleaned = %q, want %q", cleaned, "Here is my answer.")
	}
	if update != "User prefers brief answers." {
		t.Errorf("update = %q, want %q", update, "User prefers brief answers.")
	}
}

func TestExtractUserUpdate_NotFound(t *testing.T) {
	text := "Just a normal response."
	cleaned, update := extractUserUpdate(text)
	if cleaned != text {
		t.Errorf("cleaned = %q, want original text", cleaned)
	}
	if update != "" {
		t.Errorf("update = %q, want empty", update)
	}
}

func TestExtractUserUpdate_MissingCloseTag(t *testing.T) {
	text := "Answer.\n\n[USER_UPDATE]\nSome content without close tag."
	cleaned, update := extractUserUpdate(text)
	if cleaned != text {
		t.Errorf("cleaned should be unchanged when close tag is missing")
	}
	if update != "" {
		t.Errorf("update = %q, want empty", update)
	}
}

func TestEngine_Chat_UserUpdate_Supervised_SubmitsApproval(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Create a persona dir with USER.md so Save can target it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Got it!\n\n[USER_UPDATE]\nUser likes short answers.\n[/USER_UPDATE]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	approvalMgr := approval.NewManager(approvalStore, testLogger())

	engine := NewEngine("default", router, store, nil, permissions, p, "", nil, nil, approvalMgr, testLogger())

	_, err = engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		Text:       "Remember my preference",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// USER.md should NOT have been written yet (pending approval).
	if _, readErr := os.ReadFile(filepath.Join(dir, "USER.md")); readErr == nil {
		t.Error("USER.md should not exist yet — approval is pending")
	}

	// A pending approval should exist.
	pending, err := approvalMgr.List(context.Background(), approval.StatusPending)
	if err != nil {
		t.Fatalf("List approvals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
	if pending[0].Kind != approval.ActionKindUserUpdate {
		t.Errorf("kind = %q, want user_update", pending[0].Kind)
	}
}

func TestEngine_Chat_UserUpdate_Autonomous_WritesDirectly(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Done!\n\n[USER_UPDATE]\nUser is a Go developer.\n[/USER_UPDATE]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, nil, permissions, p, "", nil, nil, nil, testLogger())

	_, err = engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		Text:       "Remember I am a Go developer",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// USER.md should have been written directly (autonomous tier).
	data, err := os.ReadFile(filepath.Join(dir, "USER.md"))
	if err != nil {
		t.Fatalf("USER.md not written: %v", err)
	}
	if !strings.Contains(string(data), "User is a Go developer.") {
		t.Errorf("USER.md = %q, want it to contain the update", string(data))
	}
}

func TestEngine_Chat_UserUpdate_NoApprovalManager_DropsDirective(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Sure!\n\n[USER_UPDATE]\nSome update.\n[/USER_UPDATE]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// approvals=nil — directive should be silently stripped.
	sent := &sentMessages{}
	engine := NewEngine("default", router, store, sent.send, permissions, p, "", nil, nil, nil, testLogger())

	responseText, err := engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		Text:       "Remember something",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Directive should be stripped from response.
	if strings.Contains(responseText, "USER_UPDATE") {
		t.Errorf("response should not contain USER_UPDATE tags, got: %q", responseText)
	}

	// USER.md should not have been written.
	if _, readErr := os.ReadFile(filepath.Join(dir, "USER.md")); readErr == nil {
		t.Error("USER.md should not exist — no approval manager")
	}
}

func TestEngine_HandleMessage_WithPendingApproval_AttachesButtons(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Noted!\n\n[USER_UPDATE]\nUser likes Go.\n[/USER_UPDATE]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	approvalMgr := approval.NewManager(approvalStore, testLogger())

	sent := &sentMessages{}
	engine := NewEngine("default", router, store, sent.send, permissions, p, "", nil, nil, approvalMgr, testLogger())

	err = engine.HandleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		Text:       "Remember I like Go",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if len(sent.msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent.msgs))
	}
	msg := sent.msgs[0]
	if len(msg.Buttons) != 2 {
		t.Fatalf("buttons count = %d, want 2", len(msg.Buttons))
	}
	if msg.Buttons[0].Label != "✅ Approve" {
		t.Errorf("buttons[0].Label = %q, want '✅ Approve'", msg.Buttons[0].Label)
	}
	if msg.Buttons[1].Label != "❌ Deny" {
		t.Errorf("buttons[1].Label = %q, want '❌ Deny'", msg.Buttons[1].Label)
	}
	if !strings.HasSuffix(msg.Buttons[0].CallbackData, ":approve") {
		t.Errorf("approve button callback = %q, want :approve suffix", msg.Buttons[0].CallbackData)
	}
	if !strings.HasSuffix(msg.Buttons[1].CallbackData, ":deny") {
		t.Errorf("deny button callback = %q, want :deny suffix", msg.Buttons[1].CallbackData)
	}
}

// ---------------------------------------------------------------------------
// SKILL_CREATE directive tests
// ---------------------------------------------------------------------------

const testSkillPayload = `+++
name = "test-skill"
description = "A test skill for unit testing."
version = "1.0.0"
triggers = ["command:test-skill"]
+++

# Test Skill

When the user runs /test-skill, do something useful.`

func TestExtractSkillCreate_Found(t *testing.T) {
	text := "I'll create that skill.\n\n[SKILL_CREATE]\n" + testSkillPayload + "\n[/SKILL_CREATE]"
	cleaned, payload := extractSkillCreate(text)
	if !strings.Contains(cleaned, "I'll create that skill.") {
		t.Errorf("cleaned = %q, should contain the message text", cleaned)
	}
	if strings.Contains(cleaned, "SKILL_CREATE") {
		t.Error("cleaned should not contain SKILL_CREATE tags")
	}
	if !strings.Contains(payload, `name = "test-skill"`) {
		t.Errorf("payload = %q, should contain skill frontmatter", payload)
	}
}

func TestExtractSkillCreate_NotFound(t *testing.T) {
	text := "Just a normal response."
	cleaned, payload := extractSkillCreate(text)
	if cleaned != text {
		t.Errorf("cleaned = %q, want original text", cleaned)
	}
	if payload != "" {
		t.Errorf("payload = %q, want empty", payload)
	}
}

func TestEngine_SkillCreate_Autonomous_WritesFile(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	skillsDir := filepath.Join(dir, "skills")

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "I'll create that skill for you!\n\n[SKILL_CREATE]\n" + testSkillPayload + "\n[/SKILL_CREATE]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, nil, permissions, p, "", nil, nil, nil, testLogger())
	engine.SetSkillDirs(skillsDir, "")

	_, err = engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		Text:       "Create a test skill",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Skill file should have been written.
	skillFile := filepath.Join(skillsDir, "test-skill.md")
	data, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("skill file not created at %s: %v", skillFile, err)
	}
	if !strings.Contains(string(data), `name = "test-skill"`) {
		t.Errorf("skill file = %q, want it to contain the frontmatter", string(data))
	}

	// Skill should appear in the engine's in-memory list.
	skills := engine.Skills()
	found := false
	for _, s := range skills {
		if s.Name == "test-skill" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(skills))
		for i, s := range skills {
			names[i] = s.Name
		}
		t.Errorf("test-skill not found in engine skills: %v", names)
	}
}

func TestEngine_SkillCreate_Supervised_SubmitsApproval(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	skillsDir := filepath.Join(dir, "skills")

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "I'll create that skill pending approval.\n\n[SKILL_CREATE]\n" + testSkillPayload + "\n[/SKILL_CREATE]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	approvalMgr := approval.NewManager(approvalStore, testLogger())

	engine := NewEngine("default", router, store, nil, permissions, p, "", nil, nil, approvalMgr, testLogger())
	engine.SetSkillDirs(skillsDir, "")

	_, err = engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		Text:       "Create a test skill",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Skill file should NOT exist yet (pending approval).
	if _, readErr := os.ReadFile(filepath.Join(skillsDir, "test-skill.md")); readErr == nil {
		t.Error("skill file should not exist yet — approval is pending")
	}

	// A pending approval should exist with the correct kind.
	pending, err := approvalMgr.List(context.Background(), approval.StatusPending)
	if err != nil {
		t.Fatalf("List approvals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
	if pending[0].Kind != approval.ActionKindCreateSkill {
		t.Errorf("kind = %q, want create_skill", pending[0].Kind)
	}

	// Approving should write the file.
	if _, resolveErr := approvalMgr.Resolve(context.Background(), pending[0].ID, true, "test"); resolveErr != nil {
		t.Fatalf("Resolve: %v", resolveErr)
	}
	if _, readErr := os.ReadFile(filepath.Join(skillsDir, "test-skill.md")); readErr != nil {
		t.Errorf("skill file should exist after approval: %v", readErr)
	}

	// And the skill should appear in the engine's in-memory list.
	found := false
	for _, s := range engine.Skills() {
		if s.Name == "test-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Error("test-skill not found in engine skills after approval")
	}
}

func TestEngine_SkillCreate_Restricted_DropsDirective(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	skillsDir := filepath.Join(dir, "skills")

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "OK\n\n[SKILL_CREATE]\n" + testSkillPayload + "\n[/SKILL_CREATE]",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	permissions, err := security.NewPermissionEngine("restricted")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine("default", router, store, nil, permissions, p, "", nil, nil, nil, testLogger())
	engine.SetSkillDirs(skillsDir, "")

	responseText, err := engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		Text:       "Create skill",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Directive should be stripped from response.
	if strings.Contains(responseText, "SKILL_CREATE") {
		t.Errorf("response should not contain SKILL_CREATE tags, got: %q", responseText)
	}

	// Skill file should not exist.
	if _, readErr := os.ReadFile(filepath.Join(skillsDir, "test-skill.md")); readErr == nil {
		t.Error("skill file should not exist in restricted tier")
	}
}

func TestEngine_SkillCreate_NoSkillsDir_DropsDirective(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "OK\n\n[SKILL_CREATE]\n" + testSkillPayload + "\n[/SKILL_CREATE]",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// No SetSkillDirs call — engine.agentSkillsDir is empty.
	engine := NewEngine("default", router, store, nil, permissions, nil, "Fallback.", nil, nil, nil, testLogger())

	responseText, err := engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		Text:       "Create skill",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat should not error even without skills dir: %v", err)
	}

	// Directive should be stripped; no file written, no error.
	if strings.Contains(responseText, "SKILL_CREATE") {
		t.Errorf("response should not contain SKILL_CREATE tags, got: %q", responseText)
	}
}

// ---------------------------------------------------------------------------
// SCHEDULE_ADD directive tests
// ---------------------------------------------------------------------------

const testSchedulePayload = `name = "test-daily"
schedule = "@daily"
skill = "briefing"
channel = "telegram:123456789"
session_mode = "isolated"`

func TestExtractScheduleAdd_Found(t *testing.T) {
	text := "I'll set up that schedule.\n\n[SCHEDULE_ADD]\n" + testSchedulePayload + "\n[/SCHEDULE_ADD]"
	cleaned, payload := extractScheduleAdd(text)
	if !strings.Contains(cleaned, "I'll set up that schedule.") {
		t.Errorf("cleaned = %q, should contain the message text", cleaned)
	}
	if strings.Contains(cleaned, "SCHEDULE_ADD") {
		t.Error("cleaned should not contain SCHEDULE_ADD tags")
	}
	if !strings.Contains(payload, `name = "test-daily"`) {
		t.Errorf("payload = %q, should contain schedule TOML", payload)
	}
}

func TestEngine_ScheduleAdd_Autonomous_RegistersSchedule(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Schedule added!\n\n[SCHEDULE_ADD]\n" + testSchedulePayload + "\n[/SCHEDULE_ADD]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	sched := scheduler.New(testLogger())

	engine := NewEngine("default", router, store, nil, permissions, p, "", nil, nil, nil, testLogger())
	engine.SetScheduler(sched)

	_, err = engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		Text:       "Add a daily schedule",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// The schedule should be registered.
	entries := sched.Entries()
	found := false
	for _, e := range entries {
		if e.Name == "test-daily" {
			found = true
			if e.Skill != "briefing" {
				t.Errorf("skill = %q, want briefing", e.Skill)
			}
			if e.Channel != "telegram:123456789" {
				t.Errorf("channel = %q, want telegram:123456789", e.Channel)
			}
			break
		}
	}
	if !found {
		t.Error("test-daily schedule not found in scheduler")
	}
}

func TestEngine_ScheduleAdd_Supervised_SubmitsApproval(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Test soul."), 0644); err != nil {
		t.Fatalf("writing SOUL.md: %v", err)
	}
	p, err := persona.Load(dir)
	if err != nil {
		t.Fatalf("loading persona: %v", err)
	}

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Schedule pending approval.\n\n[SCHEDULE_ADD]\n" + testSchedulePayload + "\n[/SCHEDULE_ADD]",
			TokensUsed: llm.TokenUsage{Total: 20},
		},
	})

	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	approvalStore, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating approval store: %v", err)
	}
	approvalMgr := approval.NewManager(approvalStore, testLogger())

	sched := scheduler.New(testLogger())

	engine := NewEngine("default", router, store, nil, permissions, p, "", nil, nil, approvalMgr, testLogger())
	engine.SetScheduler(sched)

	_, err = engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		Text:       "Add a daily schedule",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Schedule should NOT be registered yet.
	if len(sched.Entries()) != 0 {
		t.Errorf("scheduler entries = %d, want 0 (pending approval)", len(sched.Entries()))
	}

	// A pending approval should exist.
	pending, err := approvalMgr.List(context.Background(), approval.StatusPending)
	if err != nil {
		t.Fatalf("List approvals: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
	if pending[0].Kind != approval.ActionKindModifySchedule {
		t.Errorf("kind = %q, want modify_schedule", pending[0].Kind)
	}

	// Approving should register the schedule.
	if _, resolveErr := approvalMgr.Resolve(context.Background(), pending[0].ID, true, "test"); resolveErr != nil {
		t.Fatalf("Resolve: %v", resolveErr)
	}
	entries := sched.Entries()
	found := false
	for _, e := range entries {
		if e.Name == "test-daily" {
			found = true
			break
		}
	}
	if !found {
		t.Error("test-daily schedule not found in scheduler after approval")
	}
}

func TestEngine_ScheduleAdd_NoScheduler_DropsDirective(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Done.\n\n[SCHEDULE_ADD]\n" + testSchedulePayload + "\n[/SCHEDULE_ADD]",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// No SetScheduler call — engine.sched is nil.
	engine := NewEngine("default", router, store, nil, permissions, nil, "Fallback.", nil, nil, nil, testLogger())

	responseText, err := engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		Text:       "Add daily schedule",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat should not error without scheduler: %v", err)
	}

	// Directive should be stripped from response.
	if strings.Contains(responseText, "SCHEDULE_ADD") {
		t.Errorf("response should not contain SCHEDULE_ADD tags, got: %q", responseText)
	}
}

func TestEngine_ScheduleAdd_InvalidExpression_LogsWarning(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	invalidPayload := `name = "bad-schedule"
schedule = "not-valid-cron"
channel = "telegram:123"
`

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Done.\n\n[SCHEDULE_ADD]\n" + invalidPayload + "\n[/SCHEDULE_ADD]",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	sched := scheduler.New(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "Fallback.", nil, nil, nil, testLogger())
	engine.SetScheduler(sched)

	// Should not return an error — invalid schedule is logged as a warning.
	responseText, err := engine.Chat(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		Text:       "Add schedule",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("Chat should not error on invalid schedule expression: %v", err)
	}

	if strings.Contains(responseText, "SCHEDULE_ADD") {
		t.Errorf("directive tags should be stripped from response, got: %q", responseText)
	}

	// No entries should be registered.
	if len(sched.Entries()) != 0 {
		t.Errorf("scheduler entries = %d, want 0 (invalid expression)", len(sched.Entries()))
	}
}
