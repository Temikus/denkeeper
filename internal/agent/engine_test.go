package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
)

// mockAdapter implements adapter.Adapter for testing.
type mockAdapter struct {
	name     string
	sent     []adapter.OutgoingMessage
	incoming chan<- adapter.IncomingMessage
}

func (m *mockAdapter) Name() string { return m.name }
func (m *mockAdapter) Start(_ context.Context, incoming chan<- adapter.IncomingMessage) error {
	m.incoming = incoming
	// Block until context cancelled (simulated by test)
	select {}
}
func (m *mockAdapter) Send(_ context.Context, msg adapter.OutgoingMessage) error {
	m.sent = append(m.sent, msg)
	return nil
}
func (m *mockAdapter) Stop() error { return nil }

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

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	permissions := security.NewPermissionEngine()

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, "You are a test assistant.", logger)

	ctx := context.Background()
	msg := adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-123",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hi there",
		Timestamp:  time.Now(),
	}

	if err := engine.handleMessage(ctx, msg); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	// Check response was sent
	if len(ma.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(ma.sent))
	}
	if ma.sent[0].Text != "Hello from Denkeeper!" {
		t.Errorf("sent text = %q, want Hello from Denkeeper!", ma.sent[0].Text)
	}
	if ma.sent[0].ExternalID != "chat-123" {
		t.Errorf("sent external_id = %q, want chat-123", ma.sent[0].ExternalID)
	}

	// Check messages were stored
	convID := "test:chat-123"
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

	callCount := 0
	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Response",
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	permissions := security.NewPermissionEngine()

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, "You are a test assistant.", logger)

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
		if err := engine.handleMessage(ctx, msg); err != nil {
			t.Fatalf("handleMessage %d: %v", i, err)
		}
		callCount++
	}

	// Should have 6 messages stored (3 user + 3 assistant)
	messages, err := store.GetMessages(ctx, "test:chat-1", 100)
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

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create a permission engine that denies "chat"
	permissions := &security.PermissionEngine{}

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, "You are a test assistant.", logger)

	err = engine.handleMessage(context.Background(), adapter.IncomingMessage{
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
	if len(ma.sent) != 0 {
		t.Errorf("sent %d messages, want 0 (should not send on denied permission)", len(ma.sent))
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

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	permissions := security.NewPermissionEngine()

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, "You are a test assistant.", logger)

	err = engine.handleMessage(context.Background(), adapter.IncomingMessage{
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
	if len(ma.sent) != 0 {
		t.Errorf("sent %d messages, want 0 (should not send on LLM error)", len(ma.sent))
	}
}

func TestEngine_HandleMessage_UnknownAdapter(t *testing.T) {
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

	ma := &mockAdapter{name: "discord"} // adapter named "discord"
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	permissions := security.NewPermissionEngine()

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, "You are a test assistant.", logger)

	// Message claims to be from "telegram" — no matching adapter
	err = engine.handleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hello",
		Timestamp:  time.Now(),
	})
	// Should not panic; currently silently succeeds (no adapter match)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ma.sent) != 0 {
		t.Errorf("sent %d messages, want 0 (no adapter match)", len(ma.sent))
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

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	permissions := security.NewPermissionEngine()

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, "You are a test assistant.", logger)

	// Empty text should be handled gracefully
	err = engine.handleMessage(context.Background(), adapter.IncomingMessage{
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
	if len(ma.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(ma.sent))
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

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	permissions := security.NewPermissionEngine()

	customPrompt := "You are a custom persona with special instructions."
	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, customPrompt, logger)

	err = engine.handleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hello",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
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
