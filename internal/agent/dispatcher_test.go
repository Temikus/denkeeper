package agent

import (
	"context"
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

	costTracker := llm.NewCostTracker(10.0)
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
func (m *mockAdapter) Stop() error { return nil }

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
