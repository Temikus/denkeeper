package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
)

func newTestEngine(t *testing.T, name string, sent *sentMessages) *Engine {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store for %s: %v", name, err)
	}
	t.Cleanup(func() { _ = store.Close() })

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(&mockProvider{
		response: &llm.ChatResponse{
			Content:    "Response from " + name,
			TokensUsed: llm.TokenUsage{Total: 10},
		},
	})

	perms, err := security.NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("creating permissions: %v", err)
	}

	return NewEngine(name, router, store, sent.send, perms, nil, "Agent "+name, nil, nil, nil, testLogger())
}

func TestDispatcher_ResolveAgent_SpecificBinding(t *testing.T) {
	sentDefault := &sentMessages{}
	sentWork := &sentMessages{}

	defaultEngine := newTestEngine(t, "default", sentDefault)
	workEngine := newTestEngine(t, "work", sentWork)

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine, "work": workEngine},
		[]Binding{
			{Pattern: "telegram", AgentName: "default"},
			{Pattern: "telegram:99999", AgentName: "work"},
		},
		nil,
		testLogger(),
	)

	// Message to the specific chat should go to the "work" agent.
	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "99999",
		UserID:     "user-1",
		Text:       "Hello work",
		Timestamp:  time.Now(),
	}

	resolved := d.resolveAgent(msg)
	if resolved.Name() != "work" {
		t.Errorf("resolveAgent = %q, want work", resolved.Name())
	}
}

func TestDispatcher_ResolveAgent_WildcardBinding(t *testing.T) {
	sentDefault := &sentMessages{}
	sentWork := &sentMessages{}

	defaultEngine := newTestEngine(t, "default", sentDefault)
	workEngine := newTestEngine(t, "work", sentWork)

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine, "work": workEngine},
		[]Binding{
			{Pattern: "telegram", AgentName: "default"},
			{Pattern: "telegram:99999", AgentName: "work"},
		},
		nil,
		testLogger(),
	)

	// Message to an unbound chat should use wildcard → "default".
	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "11111",
		UserID:     "user-1",
		Text:       "Hello default",
		Timestamp:  time.Now(),
	}

	resolved := d.resolveAgent(msg)
	if resolved.Name() != "default" {
		t.Errorf("resolveAgent = %q, want default", resolved.Name())
	}
}

func TestDispatcher_ResolveAgent_FallbackToDefault(t *testing.T) {
	sentDefault := &sentMessages{}

	defaultEngine := newTestEngine(t, "default", sentDefault)

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil, // no bindings
		nil,
		testLogger(),
	)

	// No bindings at all — should fall back to "default".
	msg := adapter.IncomingMessage{
		Adapter:    "unknown",
		ExternalID: "12345",
		UserID:     "user-1",
		Text:       "Hello",
		Timestamp:  time.Now(),
	}

	resolved := d.resolveAgent(msg)
	if resolved == nil {
		t.Fatal("resolveAgent returned nil, expected default engine")
	}
	if resolved.Name() != "default" {
		t.Errorf("resolveAgent = %q, want default", resolved.Name())
	}
}

func TestDispatcher_Dispatch_ByName(t *testing.T) {
	sentDefault := &sentMessages{}
	sentWork := &sentMessages{}

	defaultEngine := newTestEngine(t, "default", sentDefault)
	workEngine := newTestEngine(t, "work", sentWork)

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine, "work": workEngine},
		nil,
		nil,
		testLogger(),
	)

	ctx := context.Background()
	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		UserID:     "user-1",
		Text:       "Scheduled task",
		Timestamp:  time.Now(),
	}

	// Dispatch to "work" agent directly.
	if err := d.Dispatch(ctx, "work", msg); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if len(sentWork.msgs) != 1 {
		t.Fatalf("work agent sent %d messages, want 1", len(sentWork.msgs))
	}
	if len(sentDefault.msgs) != 0 {
		t.Errorf("default agent sent %d messages, want 0", len(sentDefault.msgs))
	}
}

func TestDispatcher_Dispatch_UnknownAgent(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		nil,
		testLogger(),
	)

	err := d.Dispatch(context.Background(), "nonexistent", adapter.IncomingMessage{Text: "hello"})
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

// mockAdapter implements adapter.Adapter for dispatcher send tests.
type mockAdapter struct {
	name     string
	sent     []adapter.OutgoingMessage
	incoming chan<- adapter.IncomingMessage
}

func (m *mockAdapter) Name() string { return m.name }
func (m *mockAdapter) Start(_ context.Context, incoming chan<- adapter.IncomingMessage) error {
	m.incoming = incoming
	select {} // block
}
func (m *mockAdapter) Send(_ context.Context, msg adapter.OutgoingMessage) error {
	m.sent = append(m.sent, msg)
	return nil
}
func (m *mockAdapter) SendTyping(_ context.Context, _ string) error { return nil }
func (m *mockAdapter) Stop() error                                  { return nil }

func TestDispatcher_SendFor(t *testing.T) {
	ma := &mockAdapter{name: "telegram"}

	d := NewDispatcher(
		nil,
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
	)

	sendFn := d.SendFor("telegram")
	err := sendFn(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "Hello!",
	})
	if err != nil {
		t.Fatalf("sendFn: %v", err)
	}

	if len(ma.sent) != 1 {
		t.Fatalf("adapter sent %d messages, want 1", len(ma.sent))
	}
	if ma.sent[0].Text != "Hello!" {
		t.Errorf("sent text = %q, want Hello!", ma.sent[0].Text)
	}
}

func TestDispatcher_SendFor_UnknownAdapter(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, testLogger())

	sendFn := d.SendFor("nonexistent")
	err := sendFn(context.Background(), adapter.OutgoingMessage{Text: "hello"})
	if err == nil {
		t.Fatal("expected error for unknown adapter")
	}
}

// concurrentMockProvider returns a fresh *llm.ChatResponse per call, avoiding
// data races when multiple goroutines share the same provider instance.
type concurrentMockProvider struct{}

func (p *concurrentMockProvider) Name() string { return "fresh" }
func (p *concurrentMockProvider) ChatCompletion(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content:      "ok",
		TokensUsed:   llm.TokenUsage{Total: 1},
		Model:        "test-model",
		FinishReason: "stop",
	}, nil
}
func (p *concurrentMockProvider) HealthCheck(_ context.Context) error { return nil }

// threadSafeMockAdapter is a mock adapter with mutex-protected sent slice,
// suitable for use in concurrent dispatcher tests.
type threadSafeMockAdapter struct {
	name string
	mu   sync.Mutex
	sent []adapter.OutgoingMessage
}

func (m *threadSafeMockAdapter) Name() string { return m.name }
func (m *threadSafeMockAdapter) Start(_ context.Context, _ chan<- adapter.IncomingMessage) error {
	select {} // block
}
func (m *threadSafeMockAdapter) Send(_ context.Context, msg adapter.OutgoingMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}
func (m *threadSafeMockAdapter) SendTyping(_ context.Context, _ string) error { return nil }
func (m *threadSafeMockAdapter) Stop() error                                  { return nil }
func (m *threadSafeMockAdapter) Sent() []adapter.OutgoingMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]adapter.OutgoingMessage, len(m.sent))
	copy(out, m.sent)
	return out
}

func TestDispatcher_Run_ConcurrentMessages(t *testing.T) {
	// Verify that the dispatcher processes multiple messages concurrently
	// rather than sequentially. We submit N messages via a mock adapter
	// and count how many responses arrive on a thread-safe counter.
	const n = 5

	var sendCount atomic.Int64
	threadSafeSend := func(_ context.Context, _ adapter.OutgoingMessage) error {
		sendCount.Add(1)
		return nil
	}

	// Use a file-based store: in-memory SQLite uses per-connection databases,
	// so concurrent goroutines see isolated empty stores and all writes fail.
	store, err := NewSQLiteMemoryStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Use a provider that returns a fresh response per call — the shared
	// mockProvider pointer races when concurrent goroutines modify the response.
	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 10.0}, nil)
	router := llm.NewRouter("fresh", "test-model", costTracker)
	router.RegisterProvider(&concurrentMockProvider{})

	perms, _ := security.NewPermissionEngine("autonomous")
	defaultEngine := NewEngine("default", router, store, threadSafeSend, perms, nil, "Agent", nil, nil, nil, testLogger())

	ma := &threadSafeMockAdapter{name: "telegram"}
	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		if err := d.Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("dispatcher Run error: %v", err)
		}
	}()

	// Give the dispatcher goroutine a moment to start.
	time.Sleep(10 * time.Millisecond)

	// Push n messages into the incoming channel.
	for i := range n {
		d.incoming <- adapter.IncomingMessage{
			Adapter:    "telegram",
			ExternalID: fmt.Sprintf("chat%d", i),
			UserID:     "user-1",
			Text:       fmt.Sprintf("hello %d", i),
			Timestamp:  time.Now(),
		}
	}

	// Wait until all n messages have been processed (each sends one reply).
	deadline := time.After(4 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for all %d messages; got %d", n, sendCount.Load())
		default:
		}
		if sendCount.Load() >= int64(n) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatcher_SendErrorFeedback_OnEngineFailure(t *testing.T) {
	// sendErrorFeedback should send a message to the adapter when an engine
	// error occurs, preventing silent failure.
	ma := &threadSafeMockAdapter{name: "telegram"}

	d := NewDispatcher(nil, nil, []adapter.Adapter{ma}, testLogger())

	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "99999",
	}
	d.sendErrorFeedback(context.Background(), msg)

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 error message sent, got %d", len(sent))
	}
	if sent[0].ExternalID != "99999" {
		t.Errorf("error message sent to wrong chat ID %q", sent[0].ExternalID)
	}
	if sent[0].Text == "" {
		t.Error("expected non-empty error message text")
	}
}

func TestDispatcher_SendErrorFeedback_UnknownAdapter(t *testing.T) {
	// sendErrorFeedback should be a no-op when the adapter is not registered.
	d := NewDispatcher(nil, nil, nil, testLogger())

	msg := adapter.IncomingMessage{Adapter: "nonexistent", ExternalID: "123"}
	// Should not panic.
	d.sendErrorFeedback(context.Background(), msg)
}
