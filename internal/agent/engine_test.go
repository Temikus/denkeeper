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
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/persona"
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
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "You are a test assistant.", "", logger)

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
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "You are a test assistant.", "", logger)

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

	// Create a permission engine that denies everything.
	permissions := security.NewDenyAll()

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "You are a test assistant.", "", logger)

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
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "You are a test assistant.", "", logger)

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
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "You are a test assistant.", "", logger)

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
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "You are a test assistant.", "", logger)

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

func TestEngine_Dispatch(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Scheduled response",
			TokensUsed: llm.TokenUsage{Total: 5},
		},
	})

	ma := &mockAdapter{name: "telegram"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "You are a test assistant.", "", logger)

	ctx := context.Background()
	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		UserName:   "scheduler",
		Text:       "[Scheduled: daily-briefing]",
		Timestamp:  time.Now(),
	}

	if err := engine.Dispatch(ctx, msg); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// Drain the channel and process the dispatched message directly.
	select {
	case dispatched := <-engine.incoming:
		if err := engine.handleMessage(ctx, dispatched); err != nil {
			t.Fatalf("handleMessage: %v", err)
		}
	default:
		t.Fatal("expected message in incoming channel after Dispatch")
	}

	if len(ma.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(ma.sent))
	}
	if ma.sent[0].ExternalID != "12345" {
		t.Errorf("ExternalID = %q, want 12345", ma.sent[0].ExternalID)
	}
}

func TestEngine_Dispatch_IsolatedSession(t *testing.T) {
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

	ma := &mockAdapter{name: "telegram"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "You are a test assistant.", "", logger)

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

	if err := engine.handleMessage(ctx, msg1); err != nil {
		t.Fatalf("handleMessage msg1: %v", err)
	}
	if err := engine.handleMessage(ctx, msg2); err != nil {
		t.Fatalf("handleMessage msg2: %v", err)
	}

	// Each isolated session has its own conversation with exactly 2 messages
	// (1 user + 1 assistant) — no shared history.
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
	sharedMsgs, err := store.GetMessages(ctx, "telegram:12345", 100)
	if err != nil {
		t.Fatalf("GetMessages shared: %v", err)
	}
	if len(sharedMsgs) != 0 {
		t.Errorf("shared conversation has %d messages, want 0", len(sharedMsgs))
	}
}

func TestEngine_Dispatch_ContextCancelled(t *testing.T) {
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	defer func() { _ = store.Close() }()

	costTracker := llm.NewCostTracker(10.0)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{Content: "OK"},
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	engine := NewEngine(router, store, nil, permissions, nil, "", "", logger)

	// Fill the incoming channel to capacity so Dispatch would block.
	for i := 0; i < cap(engine.incoming); i++ {
		engine.incoming <- adapter.IncomingMessage{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	err = engine.Dispatch(ctx, adapter.IncomingMessage{Text: "blocked"})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
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
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	customPrompt := "You are a custom persona with special instructions."
	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, customPrompt, "", logger)

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
	// The before/after portions are joined and trimmed; extra newlines are expected.
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

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, p, "", "", logger)

	err = engine.handleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		UserName:   "testuser",
		Text:       "Hi",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	// The sent message should have the memory directive stripped.
	if len(ma.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(ma.sent))
	}
	if ma.sent[0].Text != "Hello!" {
		t.Errorf("sent text = %q, want %q", ma.sent[0].Text, "Hello!")
	}

	// The stored message should also be stripped.
	msgs, err := store.GetMessages(context.Background(), "test:chat-1", 100)
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

	ma := &mockAdapter{name: "test"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	permissions, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	// No persona — memory update should be stripped but not persisted.
	engine := NewEngine(router, store, []adapter.Adapter{ma}, permissions, nil, "Fallback.", "", logger)

	err = engine.handleMessage(context.Background(), adapter.IncomingMessage{
		Adapter:    "test",
		ExternalID: "chat-1",
		UserID:     "user-1",
		Text:       "Hi",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("handleMessage: %v", err)
	}

	// Directive should still be stripped from the user-facing message.
	if ma.sent[0].Text != "Hello!" {
		t.Errorf("sent text = %q, want %q", ma.sent[0].Text, "Hello!")
	}
}
