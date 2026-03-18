package agent

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Temikus/foxbox/internal/adapter"
	"github.com/Temikus/foxbox/internal/llm"
	"github.com/Temikus/foxbox/internal/security"
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
	response *llm.ChatResponse
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) ChatCompletion(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return m.response, nil
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
			Content:      "Hello from Foxbox!",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
			FinishReason: "stop",
		},
	})

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	permissions := security.NewPermissionEngine()

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, logger)

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
	if ma.sent[0].Text != "Hello from Foxbox!" {
		t.Errorf("sent text = %q, want Hello from Foxbox!", ma.sent[0].Text)
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
	if messages[1].Role != "assistant" || messages[1].Content != "Hello from Foxbox!" {
		t.Errorf("message[1] = %+v, want assistant/Hello from Foxbox!", messages[1])
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

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, logger)

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
