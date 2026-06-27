package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
)

// toolThenFailProvider returns a tool_calls response on the first call, then
// fails on the second: it optionally streams chunks, optionally cancels the
// caller's context (simulating a deadline/disconnect mid-loop), and returns
// failErr.
type toolThenFailProvider struct {
	chunks  []string
	cancel  context.CancelFunc
	failErr error
	calls   int
}

func (p *toolThenFailProvider) Name() string                        { return "tool-then-fail-mock" }
func (p *toolThenFailProvider) SupportsStreaming() bool             { return true }
func (p *toolThenFailProvider) HealthCheck(_ context.Context) error { return nil }
func (p *toolThenFailProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &llm.ChatResponse{
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
		}, nil
	}
	if req.OnStream != nil {
		for _, c := range p.chunks {
			req.OnStream(llm.StreamChunk{ContentDelta: c})
		}
	}
	if p.cancel != nil {
		p.cancel()
	}
	return nil, p.failErr
}

// newInterruptTestEngine builds an autonomous engine with an empty tool
// manager (tool calls fail but are recorded) backed by an in-memory store.
func newInterruptTestEngine(t *testing.T, provider llm.Provider) (*Engine, *SQLiteMemoryStore) {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter(provider.Name(), "test-model", costTracker)
	router.RegisterProvider(provider)

	permissions, err := security.NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}
	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, nil, permissions, nil, "You are a test assistant.", nil, toolMgr, nil, testLogger())
	return engine, store
}

func interruptTestMessage(sessionID string) adapter.IncomingMessage {
	return adapter.IncomingMessage{
		Adapter:        "ws",
		ExternalID:     sessionID,
		ConversationID: sessionID,
		Text:           "What's the weather?",
		Timestamp:      time.Now(),
	}
}

func assistantMessages(t *testing.T, store *SQLiteMemoryStore, sessionID string) []StoredMessage {
	t.Helper()
	msgs, err := store.GetMessages(context.Background(), sessionID, 10)
	if err != nil {
		t.Fatalf("getting messages: %v", err)
	}
	var out []StoredMessage
	for _, m := range msgs {
		if m.Role == "assistant" {
			out = append(out, m)
		}
	}
	return out
}

func TestEngine_ChatWithEvents_ToolRecordsPersistedOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &toolThenFailProvider{cancel: cancel, failErr: context.Canceled}
	engine, store := newInterruptTestEngine(t, provider)

	sessionID := "interrupt-cancel-session"
	_, err := engine.ChatWithEvents(ctx, interruptTestMessage(sessionID), func(ChatEvent) {})
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}

	// Give the background save a moment to complete.
	time.Sleep(100 * time.Millisecond)

	records, err := store.GetToolCalls(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("getting tool calls: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d tool call records, want 1", len(records))
	}
	if records[0].ToolName != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", records[0].ToolName)
	}
	if records[0].Round != 1 {
		t.Errorf("round = %d, want 1", records[0].Round)
	}

	assistants := assistantMessages(t, store, sessionID)
	if len(assistants) != 1 {
		t.Fatalf("got %d assistant messages, want 1", len(assistants))
	}
	if !strings.Contains(assistants[0].Content, "Interrupted after 1 tool call") {
		t.Errorf("content = %q, want it to contain the interruption marker", assistants[0].Content)
	}
	if records[0].MessageID != assistants[0].ID {
		t.Errorf("record message ID = %d, want %d (the marker message)", records[0].MessageID, assistants[0].ID)
	}
}

func TestEngine_ChatWithEvents_InterruptMarkerIncludesStreamedContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &toolThenFailProvider{
		chunks:  []string{"Checking", " the weather"},
		cancel:  cancel,
		failErr: context.Canceled,
	}
	engine, store := newInterruptTestEngine(t, provider)

	sessionID := "interrupt-stream-session"
	_, err := engine.ChatWithEvents(ctx, interruptTestMessage(sessionID), func(ChatEvent) {})
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}

	time.Sleep(100 * time.Millisecond)

	assistants := assistantMessages(t, store, sessionID)
	if len(assistants) != 1 {
		t.Fatalf("got %d assistant messages, want 1", len(assistants))
	}
	content := assistants[0].Content
	if !strings.HasPrefix(content, "Checking the weather") {
		t.Errorf("content = %q, want it to start with the streamed text", content)
	}
	if !strings.Contains(content, "[Interrupted after 1 tool call") {
		t.Errorf("content = %q, want it to end with the interruption marker", content)
	}
}

func TestEngine_ChatWithEvents_ToolRecordsPersistedOnNonContextError(t *testing.T) {
	provider := &toolThenFailProvider{
		failErr: &llm.LLMError{StatusCode: 401, Message: "unauthorized"},
	}
	engine, store := newInterruptTestEngine(t, provider)

	sessionID := "interrupt-llmerror-session"
	_, err := engine.ChatWithEvents(context.Background(), interruptTestMessage(sessionID), func(ChatEvent) {})
	if err == nil {
		t.Fatal("expected error from failing provider, got nil")
	}

	time.Sleep(100 * time.Millisecond)

	records, err := store.GetToolCalls(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("getting tool calls: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d tool call records, want 1", len(records))
	}

	assistants := assistantMessages(t, store, sessionID)
	if len(assistants) != 1 {
		t.Fatalf("got %d assistant messages, want 1", len(assistants))
	}
	if !strings.Contains(assistants[0].Content, "Interrupted after 1 tool call") {
		t.Errorf("content = %q, want interruption marker", assistants[0].Content)
	}
}

func TestEngine_ChatWithEvents_NoPersistWhenErrorBeforeFirstRound(t *testing.T) {
	provider := &mockProvider{err: &llm.LLMError{StatusCode: 500, Message: "boom"}}
	engine, store := newInterruptTestEngine(t, provider)

	sessionID := "interrupt-preloop-session"
	_, err := engine.ChatWithEvents(context.Background(), interruptTestMessage(sessionID), func(ChatEvent) {})
	if err == nil {
		t.Fatal("expected error from failing provider, got nil")
	}

	time.Sleep(100 * time.Millisecond)

	records, err := store.GetToolCalls(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("getting tool calls: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("got %d tool call records, want 0", len(records))
	}
	if assistants := assistantMessages(t, store, sessionID); len(assistants) != 0 {
		t.Errorf("got %d assistant messages, want 0", len(assistants))
	}
}
