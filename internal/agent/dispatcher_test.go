package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/tool"
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
	name         string
	sent         []adapter.OutgoingMessage
	incoming     chan<- adapter.IncomingMessage
	sendTypingFn func() // optional hook called by SendTyping
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
func (m *mockAdapter) SendTyping(_ context.Context, _ string) error {
	if m.sendTypingFn != nil {
		m.sendTypingFn()
	}
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

// editorMockAdapter is a thread-safe mock adapter that also implements
// adapter.MessageEditor so the dispatcher's activity log path can be
// exercised end-to-end (Dispatch → buildEventHandler → activityLog).
type editorMockAdapter struct {
	name      string
	mu        sync.Mutex
	sent      []adapter.OutgoingMessage
	edits     []adapter.OutgoingMessage
	nextMsgID int
}

func (m *editorMockAdapter) Name() string { return m.name }
func (m *editorMockAdapter) Start(_ context.Context, _ chan<- adapter.IncomingMessage) error {
	select {}
}
func (m *editorMockAdapter) Send(_ context.Context, msg adapter.OutgoingMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}
func (m *editorMockAdapter) SendTyping(_ context.Context, _ string) error { return nil }
func (m *editorMockAdapter) Stop() error                                  { return nil }
func (m *editorMockAdapter) SendAndGetID(_ context.Context, msg adapter.OutgoingMessage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	m.nextMsgID++
	return fmt.Sprintf("msg-%d", m.nextMsgID), nil
}
func (m *editorMockAdapter) EditText(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (m *editorMockAdapter) EditMessage(_ context.Context, _, _ string, msg adapter.OutgoingMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edits = append(m.edits, msg)
	return nil
}
func (m *editorMockAdapter) Sent() []adapter.OutgoingMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]adapter.OutgoingMessage, len(m.sent))
	copy(out, m.sent)
	return out
}
func (m *editorMockAdapter) Edits() []adapter.OutgoingMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]adapter.OutgoingMessage, len(m.edits))
	copy(out, m.edits)
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

func TestStartTypingTicker_SendsTypingPeriodically(t *testing.T) {
	var count atomic.Int64
	ma := &mockAdapter{name: "telegram"}
	ma.sendTypingFn = func() { count.Add(1) }

	d := NewDispatcher(nil, nil, []adapter.Adapter{ma}, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Swap the real interval for a short one so the test runs fast.
	orig := typingInterval
	typingInterval = 20 * time.Millisecond
	t.Cleanup(func() { typingInterval = orig })

	msg := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "42"}
	stop := d.startTypingTicker(ctx, msg)

	// Wait long enough for at least 2 ticks.
	time.Sleep(70 * time.Millisecond)
	stop()

	if got := count.Load(); got < 2 {
		t.Errorf("expected at least 2 typing calls, got %d", got)
	}
}

func TestStartTypingTicker_StopsAfterStop(t *testing.T) {
	var count atomic.Int64
	ma := &mockAdapter{name: "telegram"}
	ma.sendTypingFn = func() { count.Add(1) }

	d := NewDispatcher(nil, nil, []adapter.Adapter{ma}, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orig := typingInterval
	typingInterval = 20 * time.Millisecond
	t.Cleanup(func() { typingInterval = orig })

	msg := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "42"}
	stop := d.startTypingTicker(ctx, msg)
	time.Sleep(50 * time.Millisecond)
	stop()

	snapshot := count.Load()
	// Allow one in-flight tick to complete, then verify count does not grow.
	time.Sleep(50 * time.Millisecond)
	if after := count.Load(); after > snapshot+1 {
		t.Errorf("typing calls continued after stop: snapshot=%d, after=%d", snapshot, after)
	}
}

func TestStartTypingTicker_UnknownAdapter_NoOp(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, testLogger())
	msg := adapter.IncomingMessage{Adapter: "nonexistent", ExternalID: "42"}
	// Should not panic, stop should be callable.
	stop := d.startTypingTicker(context.Background(), msg)
	stop()
}

func TestStartTypingTicker_ContextCancel_Stops(t *testing.T) {
	var count atomic.Int64
	ma := &mockAdapter{name: "telegram"}
	ma.sendTypingFn = func() { count.Add(1) }

	d := NewDispatcher(nil, nil, []adapter.Adapter{ma}, testLogger())

	orig := typingInterval
	typingInterval = 20 * time.Millisecond
	t.Cleanup(func() { typingInterval = orig })

	ctx, cancel := context.WithCancel(context.Background())
	msg := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "42"}
	_ = d.startTypingTicker(ctx, msg)

	time.Sleep(50 * time.Millisecond)
	cancel()

	snapshot := count.Load()
	time.Sleep(50 * time.Millisecond)
	if after := count.Load(); after > snapshot+1 {
		t.Errorf("typing calls continued after context cancel: snapshot=%d, after=%d", snapshot, after)
	}
}

// --- activityLog tests ---

// mockMessageEditor records SendAndGetID / EditText / EditMessage calls.
type mockMessageEditor struct {
	sends    []adapter.OutgoingMessage
	edits    []editCall
	editMsgs []editMsgCall
	msgID    string // returned by the next SendAndGetID call
	msgIDs   []string
}

type editCall struct {
	externalID string
	messageID  string
	text       string
	parseMode  string
}

type editMsgCall struct {
	externalID string
	messageID  string
	msg        adapter.OutgoingMessage
}

func (m *mockMessageEditor) SendAndGetID(_ context.Context, msg adapter.OutgoingMessage) (string, error) {
	m.sends = append(m.sends, msg)
	if len(m.msgIDs) > 0 {
		id := m.msgIDs[0]
		m.msgIDs = m.msgIDs[1:]
		return id, nil
	}
	return m.msgID, nil
}

func (m *mockMessageEditor) EditText(_ context.Context, externalID, messageID, text, parseMode string) error {
	m.edits = append(m.edits, editCall{externalID, messageID, text, parseMode})
	return nil
}

func (m *mockMessageEditor) EditMessage(_ context.Context, externalID, messageID string, msg adapter.OutgoingMessage) error {
	m.editMsgs = append(m.editMsgs, editMsgCall{externalID, messageID, msg})
	return nil
}

// lastSent returns the most recent message text sent or edited via the editor.
// This is the surface the user sees in the Telegram chat.
func (m *mockMessageEditor) lastRendered() string {
	if len(m.editMsgs) > 0 {
		return m.editMsgs[len(m.editMsgs)-1].msg.Text
	}
	if len(m.sends) > 0 {
		return m.sends[len(m.sends)-1].Text
	}
	return ""
}

func newTestActivityLog(me *mockMessageEditor) *activityLog {
	return &activityLog{
		editor:     me,
		externalID: "chat-1",
		adapter:    "telegram",
		logger:     testLogger(),
	}
}

func TestActivityLog_RenderChunk_Empty(t *testing.T) {
	l := &activityLog{}
	c := &logChunk{toolIndex: map[string]int{}}
	if got := l.renderChunk(c, true); got != "" {
		t.Errorf("empty chunk render = %q, want empty", got)
	}
}

func TestActivityLog_RenderChunk_WrapsInBlockquote(t *testing.T) {
	l := &activityLog{}
	c := &logChunk{
		lines: []activityLine{
			{tool: "search", status: "auto-approved"},
			{tool: "fetch", status: "⏳"},
		},
		toolIndex: map[string]int{},
	}
	got := l.renderChunk(c, false)
	want := "📋 <b>Activity log</b>\n<blockquote expandable>" +
		"🔧 <b>search</b> — auto-approved\n🔧 <b>fetch</b> — ⏳" +
		"</blockquote>"
	if got != want {
		t.Errorf("render =\n%s\nwant:\n%s", got, want)
	}
}

func TestActivityLog_RenderChunk_AppendsPendingApproval(t *testing.T) {
	l := &activityLog{
		pending: &pendingApproval{tool: "read_file", args: `{"path":"/etc/hosts"}`, callback: "cb-1"},
	}
	c := &logChunk{
		lines:     []activityLine{{tool: "search", status: "✅ 10ms"}},
		toolIndex: map[string]int{},
	}
	got := l.renderChunk(c, true)
	want := "📋 <b>Activity log</b>\n<blockquote expandable>🔧 <b>search</b> — ✅ 10ms</blockquote>\n" +
		"🔧 <b>read_file</b> — approve?\n<blockquote expandable>{&#34;path&#34;:&#34;/etc/hosts&#34;}</blockquote>"
	if got != want {
		t.Errorf("render =\n%s\nwant:\n%s", got, want)
	}
}

func TestActivityLog_RenderChunk_PendingOnly_NoBlockquote(t *testing.T) {
	l := &activityLog{
		pending: &pendingApproval{tool: "first_tool", args: "args", callback: "cb-1"},
	}
	c := &logChunk{toolIndex: map[string]int{}}
	got := l.renderChunk(c, true)
	want := "🔧 <b>first_tool</b> — approve?\n<blockquote expandable>args</blockquote>"
	if got != want {
		t.Errorf("render =\n%s\nwant:\n%s", got, want)
	}
}

func TestActivityLog_AutoApproved_SendsNewMessage(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)

	l.autoApproved(context.Background(), "search_web")

	if len(me.sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(me.sends))
	}
	if me.sends[0].ParseMode != "HTML" {
		t.Errorf("expected HTML parse mode, got %q", me.sends[0].ParseMode)
	}
	if got := l.chunks[0].messageID; got != "msg-1" {
		t.Errorf("chunk messageID = %q, want msg-1", got)
	}
}

func TestActivityLog_ToolStartEnd_EditsInPlace(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	l.toolStart(ctx, "search")
	if len(me.sends) != 1 {
		t.Fatalf("expected 1 send after first toolStart, got %d", len(me.sends))
	}

	l.toolEnd(ctx, "search", 150, "")
	if len(me.editMsgs) != 1 {
		t.Fatalf("expected 1 edit after toolEnd, got %d", len(me.editMsgs))
	}
	if me.editMsgs[0].messageID != "msg-1" {
		t.Errorf("edit messageID = %q, want msg-1", me.editMsgs[0].messageID)
	}
	if !strings.Contains(me.lastRendered(), "🔧 <b>search</b> — ✅ 150ms") {
		t.Errorf("last rendered missing completion line: %s", me.lastRendered())
	}
}

func TestActivityLog_MultipleTools_AccumulatesLines(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	l.autoApproved(ctx, "tool_a")
	l.toolStart(ctx, "tool_a")
	l.toolEnd(ctx, "tool_a", 100, "")
	l.autoApproved(ctx, "tool_b")
	l.toolStart(ctx, "tool_b")
	l.toolEnd(ctx, "tool_b", 200, "")

	if len(me.sends) != 1 {
		t.Errorf("expected 1 send, got %d", len(me.sends))
	}
	if len(me.editMsgs) != 5 {
		t.Errorf("expected 5 edits, got %d", len(me.editMsgs))
	}
	if got := len(l.chunks[0].lines); got != 2 {
		t.Fatalf("expected 2 lines in active chunk, got %d", got)
	}
}

func TestActivityLog_ToolEnd_WithError(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	l.toolStart(ctx, "fetch")
	l.toolEnd(ctx, "fetch", 0, "connection refused")

	if !strings.Contains(me.lastRendered(), "🔧 <b>fetch</b> — ❌") {
		t.Errorf("rendered missing error line: %s", me.lastRendered())
	}
}

func TestActivityLog_ToolEnd_OrphanAppendsTerminalLine(t *testing.T) {
	// A tool_end that arrives without a matching tool_start (e.g. supervisor
	// denied execution before tool_start was emitted) must still produce a
	// visible line. The line is terminal — a subsequent event for the same
	// tool name must land on a fresh row, not overwrite the historical one.
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	l.toolEnd(ctx, "fetch", 50, "")

	c := l.chunks[0]
	if len(c.lines) != 1 {
		t.Fatalf("expected 1 line after orphan toolEnd, got %d", len(c.lines))
	}
	if c.lines[0].status != "✅ 50ms" {
		t.Errorf("status = %q, want ✅ 50ms", c.lines[0].status)
	}
	if _, ok := c.toolIndex["fetch"]; ok {
		t.Errorf("terminal line should not remain in toolIndex")
	}

	l.toolStart(ctx, "fetch")
	l.toolEnd(ctx, "fetch", 75, "")

	if got := len(l.chunks[0].lines); got != 2 {
		t.Errorf("second call should append a fresh row, got %d lines total", got)
	}
}

func TestActivityLog_StatusContainsHTML_IsEscaped(t *testing.T) {
	// Status text can carry untrusted content (e.g. a supervisor's free-text
	// deny reason). renderChunk must HTML-escape status so a stray '<' or
	// '&' cannot break Telegram's HTML parse mode.
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	l.supervisorLine(ctx, "fetch", "❌ denied: <script>&bad")

	rendered := me.lastRendered()
	if strings.Contains(rendered, "<script>") {
		t.Errorf("status containing '<script>' must be escaped: %s", rendered)
	}
	if !strings.Contains(rendered, "&lt;script&gt;&amp;bad") {
		t.Errorf("expected escaped status in rendered output: %s", rendered)
	}
}

func TestActivityLog_SameToolCalledTwice(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	l.toolStart(ctx, "search")
	l.toolEnd(ctx, "search", 100, "")
	l.toolStart(ctx, "search")
	l.toolEnd(ctx, "search", 200, "")

	c := l.chunks[0]
	if len(c.lines) != 2 {
		t.Fatalf("expected 2 lines for repeated tool, got %d", len(c.lines))
	}
	if c.lines[0].status != "✅ 100ms" {
		t.Errorf("first call status = %q", c.lines[0].status)
	}
	if c.lines[1].status != "✅ 200ms" {
		t.Errorf("second call status = %q", c.lines[1].status)
	}
}

func TestActivityLog_RenderChunk_EscapesHTML(t *testing.T) {
	l := &activityLog{}
	c := &logChunk{
		lines:     []activityLine{{tool: "<script>alert(1)</script>", status: "✅ 1ms"}},
		toolIndex: map[string]int{},
	}
	got := l.renderChunk(c, false)
	if !strings.Contains(got, "🔧 <b>&lt;script&gt;alert(1)&lt;/script&gt;</b> — ✅ 1ms") {
		t.Errorf("HTML not escaped: %s", got)
	}
}

func TestActivityLog_SetPending_AttachesButtons(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)

	l.setPending(context.Background(), "read_file", `{"path":"/etc/hosts"}`, "cb-1")

	if len(me.sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(me.sends))
	}
	sent := me.sends[0]
	if len(sent.Buttons) != 4 {
		t.Errorf("expected 4 approval buttons, got %d", len(sent.Buttons))
	}
	if sent.Buttons[0].CallbackData != "cb-1:approve" {
		t.Errorf("button[0] callback = %q", sent.Buttons[0].CallbackData)
	}
	if !strings.Contains(sent.Text, "🔧 <b>read_file</b> — approve?") {
		t.Errorf("missing approval header: %s", sent.Text)
	}
}

func TestActivityLog_ToolStartAfterPending_RemovesButtons(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	l.setPending(ctx, "read_file", "{}", "cb-1")
	if len(me.sends) != 1 || len(me.sends[0].Buttons) != 4 {
		t.Fatal("setup: expected initial send with buttons")
	}

	// Simulate user clicking approve → engine fires tool_start.
	l.toolStart(ctx, "read_file")

	last := me.editMsgs[len(me.editMsgs)-1]
	if len(last.msg.Buttons) != 0 {
		t.Errorf("expected buttons removed after toolStart, got %d", len(last.msg.Buttons))
	}
	if l.pending != nil {
		t.Errorf("expected pending cleared after toolStart, got %+v", l.pending)
	}
	if !strings.Contains(last.msg.Text, "🔧 <b>read_file</b> — ⏳") {
		t.Errorf("expected in-flight line, got: %s", last.msg.Text)
	}
}

func TestActivityLog_ToolDenied_TransitionsLine(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	l.setPending(ctx, "read_file", "{}", "cb-1")
	l.toolDenied(ctx, "read_file")

	if l.pending != nil {
		t.Errorf("expected pending cleared after deny")
	}
	if !strings.Contains(me.lastRendered(), "🔧 <b>read_file</b> — ❌ denied") {
		t.Errorf("expected denied line, got: %s", me.lastRendered())
	}
	last := me.editMsgs[len(me.editMsgs)-1]
	if len(last.msg.Buttons) != 0 {
		t.Errorf("expected buttons removed after deny")
	}
}

func TestActivityLog_OverflowSpawnsNewChunk(t *testing.T) {
	me := &mockMessageEditor{msgIDs: []string{"msg-1", "msg-2"}}
	l := newTestActivityLog(me)
	ctx := context.Background()

	// 80-char tool name × ~40 lines ≈ 3200+ chars; pushing past 3500 forces a split.
	longTool := strings.Repeat("toolname", 10) // 80 chars
	for i := 0; i < 50; i++ {
		l.toolStart(ctx, longTool+strconv.Itoa(i))
		l.toolEnd(ctx, longTool+strconv.Itoa(i), 100, "")
	}

	if len(l.chunks) < 2 {
		t.Fatalf("expected at least 2 chunks after overflow, got %d", len(l.chunks))
	}
	if len(me.sends) < 2 {
		t.Errorf("expected at least 2 SendAndGetID calls (one per chunk), got %d", len(me.sends))
	}
}

func TestActivityLog_TruncatesOversizedApprovalArgs(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)

	huge := strings.Repeat("a", 5000)
	l.setPending(context.Background(), "tool", huge, "cb-1")

	if l.pending == nil {
		t.Fatal("pending nil")
	}
	// Truncation is rune-based: argsMaxChars runes plus a single "…" rune.
	if got := utf8.RuneCountInString(l.pending.args); got != approvalArgsMaxChars+1 {
		t.Errorf("rune count = %d, want %d", got, approvalArgsMaxChars+1)
	}
	if !strings.HasSuffix(l.pending.args, "…") {
		t.Errorf("expected ellipsis suffix, got tail %q", l.pending.args[len(l.pending.args)-10:])
	}
}

func TestActivityLog_TruncatesAtRuneBoundary(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)

	// Build a payload of 3-byte UTF-8 runes that exceeds the rune cap.
	// Byte-slice truncation would split a multi-byte sequence and produce
	// invalid UTF-8; rune-based truncation must preserve every code point.
	multibyte := strings.Repeat("☃", approvalArgsMaxChars+500)
	l.setPending(context.Background(), "tool", multibyte, "cb-1")

	if !utf8.ValidString(l.pending.args) {
		t.Errorf("truncated args contain invalid UTF-8")
	}
	if got := utf8.RuneCountInString(l.pending.args); got != approvalArgsMaxChars+1 {
		t.Errorf("rune count = %d, want %d", got, approvalArgsMaxChars+1)
	}
}

func TestActivityLog_ToolDeniedUpdatesExistingLine(t *testing.T) {
	me := &mockMessageEditor{msgID: "msg-1"}
	l := newTestActivityLog(me)
	ctx := context.Background()

	// Simulate auto-approval landing first, then a deny event for the same
	// tool — the deny should transition the existing line in place rather
	// than appending a duplicate row.
	l.autoApproved(ctx, "search")
	l.toolDenied(ctx, "search")

	c := l.chunks[0]
	if len(c.lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(c.lines))
	}
	if c.lines[0].status != "❌ denied" {
		t.Errorf("status = %q, want ❌ denied", c.lines[0].status)
	}
}

// ---------------------------------------------------------------------------
// Channel routing tests
// ---------------------------------------------------------------------------

func TestDispatcher_ResolveChannel_SpecificBinding(t *testing.T) {
	sentDefault := &sentMessages{}
	sentWork := &sentMessages{}

	defaultEngine := newTestEngine(t, "default", sentDefault)
	workEngine := newTestEngine(t, "work", sentWork)

	channels := []*Channel{
		{Name: "personal", AgentName: "default", Adapters: []string{"telegram"}},
		{Name: "work", AgentName: "work", Adapters: []string{"telegram:99999"}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine, "work": workEngine},
		nil,
		nil,
		testLogger(),
		WithChannels(channels, nil),
	)

	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "99999",
		Text:       "Hello work",
		Timestamp:  time.Now(),
	}

	ch, e := d.resolveChannel(msg)
	if ch == nil || ch.Name != "work" {
		t.Fatalf("resolveChannel channel = %v, want work", ch)
	}
	if e.Name() != "work" {
		t.Errorf("resolveChannel engine = %q, want work", e.Name())
	}
}

func TestDispatcher_ResolveChannel_WildcardBinding(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	channels := []*Channel{
		{Name: "personal", AgentName: "default", Adapters: []string{"telegram"}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		nil,
		testLogger(),
		WithChannels(channels, nil),
	)

	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "11111",
		Text:       "Hello",
		Timestamp:  time.Now(),
	}

	ch, e := d.resolveChannel(msg)
	if ch == nil || ch.Name != "personal" {
		t.Fatalf("resolveChannel channel = %v, want personal", ch)
	}
	if e.Name() != "default" {
		t.Errorf("resolveChannel engine = %q, want default", e.Name())
	}
}

func TestDispatcher_ResolveChannel_ActiveOverride(t *testing.T) {
	sentDefault := &sentMessages{}
	sentWork := &sentMessages{}

	defaultEngine := newTestEngine(t, "default", sentDefault)
	workEngine := newTestEngine(t, "work", sentWork)

	channels := []*Channel{
		{Name: "personal", AgentName: "default", Adapters: []string{"telegram"}},
		{Name: "work", AgentName: "work", Adapters: []string{}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine, "work": workEngine},
		nil,
		nil,
		testLogger(),
		WithChannels(channels, nil),
	)

	// Set active override to "work" for this chat.
	d.activeChannelsMu.Lock()
	d.activeChannels["telegram:11111"] = "work"
	d.activeChannelsMu.Unlock()

	msg := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "11111",
		Text:       "Hello",
		Timestamp:  time.Now(),
	}

	ch, e := d.resolveChannel(msg)
	if ch == nil || ch.Name != "work" {
		t.Fatalf("resolveChannel channel = %v, want work (active override)", ch)
	}
	if e.Name() != "work" {
		t.Errorf("resolveChannel engine = %q, want work", e.Name())
	}
}

func TestDispatcher_ResolveChannel_FallbackToResolveAgent(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	// Channels configured but none match "discord" adapter.
	channels := []*Channel{
		{Name: "personal", AgentName: "default", Adapters: []string{"telegram"}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		[]Binding{{Pattern: "discord", AgentName: "default"}},
		nil,
		testLogger(),
		WithChannels(channels, nil),
	)

	msg := adapter.IncomingMessage{
		Adapter:    "discord",
		ExternalID: "guild-123",
		Text:       "Hello",
		Timestamp:  time.Now(),
	}

	// resolveChannel returns nil — no channel matches discord.
	ch, e := d.resolveChannel(msg)
	if ch != nil {
		t.Fatalf("resolveChannel channel = %v, want nil for discord", ch)
	}
	if e != nil {
		t.Fatalf("resolveChannel engine = %v, want nil", e)
	}

	// Legacy resolveAgent still works as fallback.
	fallback := d.resolveAgent(msg)
	if fallback == nil || fallback.Name() != "default" {
		t.Errorf("resolveAgent fallback = %v, want default", fallback)
	}
}

func TestDispatcher_ResolveChannel_NoChannels_ReturnsNil(t *testing.T) {
	// Without WithChannels, resolveChannel always returns nil.
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		[]Binding{{Pattern: "telegram", AgentName: "default"}},
		nil,
		testLogger(),
	)

	msg := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "123", Text: "Hello"}
	ch, e := d.resolveChannel(msg)
	if ch != nil || e != nil {
		t.Errorf("resolveChannel without channels = (%v, %v), want (nil, nil)", ch, e)
	}
}

func TestChannel_ConversationID(t *testing.T) {
	ch := &Channel{Name: "work"}
	if got := ch.ConversationID(); got != "chan:work" {
		t.Errorf("ConversationID() = %q, want chan:work", got)
	}
}

func TestChannel_ResolveBinding_Specific(t *testing.T) {
	ch := &Channel{Name: "work", Adapters: []string{"telegram:12345"}}
	adapter, eid, wildcard, ok := ch.ResolveBinding()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if wildcard {
		t.Error("expected wildcard=false for specific binding")
	}
	if adapter != "telegram" || eid != "12345" {
		t.Errorf("got adapter=%q eid=%q, want telegram/12345", adapter, eid)
	}
}

func TestChannel_ResolveBinding_WildcardOnly(t *testing.T) {
	ch := &Channel{Name: "work", Adapters: []string{"telegram"}}
	adapter, eid, wildcard, ok := ch.ResolveBinding()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !wildcard {
		t.Error("expected wildcard=true for wildcard-only binding")
	}
	if adapter != "telegram" {
		t.Errorf("adapter = %q, want telegram", adapter)
	}
	if eid != "" {
		t.Errorf("eid = %q, want empty", eid)
	}
}

func TestChannel_ResolveBinding_NoAdapters(t *testing.T) {
	ch := &Channel{Name: "work", Adapters: nil}
	_, _, _, ok := ch.ResolveBinding()
	if ok {
		t.Error("expected ok=false for channel with no adapters")
	}
}

func TestChannel_ResolveBinding_PrefersSpecific(t *testing.T) {
	ch := &Channel{Name: "work", Adapters: []string{"telegram", "discord:99"}}
	adapter, eid, wildcard, ok := ch.ResolveBinding()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if wildcard {
		t.Error("expected wildcard=false — should pick the specific binding")
	}
	if adapter != "discord" || eid != "99" {
		t.Errorf("got adapter=%q eid=%q, want discord/99", adapter, eid)
	}
}

func TestResolveChannelByName_Found(t *testing.T) {
	channels := map[string]*Channel{
		"work": {Name: "work", AgentName: "default", Adapters: []string{"telegram:12345"}},
	}
	convID, adapter, eid, wildcard, err := ResolveChannelByName(channels, "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if convID != "chan:work" {
		t.Errorf("convID = %q, want chan:work", convID)
	}
	if adapter != "telegram" || eid != "12345" {
		t.Errorf("got adapter=%q eid=%q, want telegram/12345", adapter, eid)
	}
	if wildcard {
		t.Error("expected wildcard=false")
	}
}

func TestResolveChannelByName_NotFound(t *testing.T) {
	channels := map[string]*Channel{}
	_, _, _, _, err := ResolveChannelByName(channels, "ghost")
	if err == nil {
		t.Fatal("expected error for missing channel")
	}
}

func TestResolveChannelByName_NilMap(t *testing.T) {
	_, _, _, _, err := ResolveChannelByName(nil, "work")
	if err == nil {
		t.Fatal("expected error for nil channel map")
	}
}

func TestChannel_IsBroadcast(t *testing.T) {
	ch := &Channel{Delivery: "broadcast"}
	if !ch.IsBroadcast() {
		t.Error("expected IsBroadcast()=true for broadcast delivery")
	}
	ch2 := &Channel{Delivery: "single"}
	if ch2.IsBroadcast() {
		t.Error("expected IsBroadcast()=false for single delivery")
	}
	ch3 := &Channel{}
	if ch3.IsBroadcast() {
		t.Error("expected IsBroadcast()=false for empty delivery")
	}
}

func TestChannel_ResolveAllBindings(t *testing.T) {
	ch := &Channel{
		Name:     "work",
		Adapters: []string{"telegram:123", "discord:456", "slack"},
	}
	bindings := ch.ResolveAllBindings()
	if len(bindings) != 2 {
		t.Fatalf("expected 2 specific bindings, got %d", len(bindings))
	}
	if bindings[0].Adapter != "telegram" || bindings[0].ExternalID != "123" {
		t.Errorf("binding[0] = %+v, want telegram:123", bindings[0])
	}
	if bindings[1].Adapter != "discord" || bindings[1].ExternalID != "456" {
		t.Errorf("binding[1] = %+v, want discord:456", bindings[1])
	}
}

func TestChannel_ResolveAllBindings_NoSpecific(t *testing.T) {
	ch := &Channel{Name: "work", Adapters: []string{"telegram"}}
	bindings := ch.ResolveAllBindings()
	if len(bindings) != 0 {
		t.Errorf("expected 0 bindings for wildcard-only, got %d", len(bindings))
	}
}

func TestResolveChannelByName_WildcardOnly(t *testing.T) {
	channels := map[string]*Channel{
		"work": {Name: "work", AgentName: "default", Adapters: []string{"telegram"}},
	}
	_, _, _, wildcard, err := ResolveChannelByName(channels, "work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wildcard {
		t.Error("expected wildcard=true for wildcard-only binding")
	}
}

func TestIsSessionCommand(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"/session", true},
		{"/session work", true},
		{"/session reset", true},
		{"  /session  ", true},
		{"/sessions", false},
		{"hello /session", false},
		{"/Session", false}, // case sensitive
		{"", false},
	}
	for _, tt := range tests {
		if got := isSessionCommand(tt.text); got != tt.want {
			t.Errorf("isSessionCommand(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestDispatcher_SessionCommand_Switch(t *testing.T) {
	sentDefault := &sentMessages{}
	sentWork := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)
	workEngine := newTestEngine(t, "work", sentWork)

	ma := &threadSafeMockAdapter{name: "telegram"}

	channels := []*Channel{
		{Name: "personal", AgentName: "default", Adapters: []string{"telegram"}},
		{Name: "work", AgentName: "work", Adapters: []string{}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine, "work": workEngine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
		WithChannels(channels, nil),
	)

	ctx := context.Background()

	// Switch to "work" channel.
	d.handleSessionCommand(ctx, adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "/session work",
	})

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	if got := sent[0].Text; got != `Switched to channel "work" (agent: work).` {
		t.Errorf("switch response = %q", got)
	}

	// Verify active channel was set.
	d.activeChannelsMu.RLock()
	active := d.activeChannels["telegram:12345"]
	d.activeChannelsMu.RUnlock()
	if active != "work" {
		t.Errorf("active channel = %q, want work", active)
	}
}

func TestDispatcher_SessionCommand_Reset(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	ma := &threadSafeMockAdapter{name: "telegram"}

	channels := []*Channel{
		{Name: "personal", AgentName: "default", Adapters: []string{"telegram"}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
		WithChannels(channels, nil),
	)

	// Pre-set an active channel.
	d.activeChannelsMu.Lock()
	d.activeChannels["telegram:12345"] = "personal"
	d.activeChannelsMu.Unlock()

	ctx := context.Background()
	d.handleSessionCommand(ctx, adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "/session reset",
	})

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	if got := sent[0].Text; got != "Session reset to default routing." {
		t.Errorf("reset response = %q", got)
	}

	// Verify active channel was cleared.
	d.activeChannelsMu.RLock()
	_, exists := d.activeChannels["telegram:12345"]
	d.activeChannelsMu.RUnlock()
	if exists {
		t.Error("active channel should have been cleared after reset")
	}
}

func TestDispatcher_SessionCommand_UnknownChannel(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	ma := &threadSafeMockAdapter{name: "telegram"}

	channels := []*Channel{
		{Name: "personal", AgentName: "default", Adapters: []string{"telegram"}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
		WithChannels(channels, nil),
	)

	d.handleSessionCommand(context.Background(), adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "/session nonexistent",
	})

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	if got := sent[0].Text; got != `Unknown channel "nonexistent". Use /session to list available channels.` {
		t.Errorf("unknown channel response = %q", got)
	}
}

func TestDispatcher_SessionCommand_List(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	ma := &threadSafeMockAdapter{name: "telegram"}

	channels := []*Channel{
		{Name: "personal", AgentName: "default", Adapters: []string{"telegram"}},
		{Name: "work", AgentName: "default", Adapters: []string{}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
		WithChannels(channels, nil),
	)

	d.handleSessionCommand(context.Background(), adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "/session",
	})

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	// Just verify it contains key fragments — exact formatting may evolve.
	if got := sent[0].Text; !contains(got, "personal") || !contains(got, "work") {
		t.Errorf("session list should mention channel names, got: %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Control command tests (/stop, /panic, /resume)
// ---------------------------------------------------------------------------

func TestControlCommand(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"/stop", "stop"},
		{"/panic", "panic"},
		{"/resume", "resume"},
		{"  /stop  ", "stop"},
		{"/stopping", ""},
		{"hello /stop", ""},
		{"/session", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := controlCommand(tt.text); got != tt.want {
			t.Errorf("controlCommand(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestDispatcher_StopChat_CancelsInFlight(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, testLogger())

	cancelled := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Manually register an in-flight request.
	d.inFlightMu.Lock()
	d.inFlight["telegram:12345"] = &inFlightRequest{
		cancel: func() {
			cancel()
			close(cancelled)
		},
		agent: "default",
		start: time.Now(),
	}
	d.inFlightMu.Unlock()

	err := d.StopChat("telegram", "12345")
	if err != nil {
		t.Fatalf("StopChat returned error: %v", err)
	}

	select {
	case <-cancelled:
		// success
	case <-time.After(time.Second):
		t.Fatal("cancel was not called")
	}

	_ = ctx // consume ctx to avoid vet warning

	// Verify entry was removed.
	d.inFlightMu.Lock()
	_, exists := d.inFlight["telegram:12345"]
	d.inFlightMu.Unlock()
	if exists {
		t.Error("in-flight entry should have been removed")
	}
}

func TestDispatcher_StopChat_NoInFlight(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, testLogger())

	err := d.StopChat("telegram", "99999")
	if err == nil {
		t.Fatal("expected error for missing in-flight request")
	}
}

func TestDispatcher_Panic_CancelsAll(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, testLogger())

	var cancelCount atomic.Int32
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("adapter:%d", i)
		d.inFlightMu.Lock()
		d.inFlight[key] = &inFlightRequest{
			cancel: func() { cancelCount.Add(1) },
			agent:  "default",
			start:  time.Now(),
		}
		d.inFlightMu.Unlock()
	}

	d.Panic()

	if got := cancelCount.Load(); got != 3 {
		t.Errorf("expected 3 cancels, got %d", got)
	}

	if !d.IsPanicked() {
		t.Error("expected IsPanicked() to be true")
	}

	d.inFlightMu.Lock()
	remaining := len(d.inFlight)
	d.inFlightMu.Unlock()
	if remaining != 0 {
		t.Errorf("expected empty in-flight map, got %d entries", remaining)
	}
}

func TestDispatcher_Resume_ClearsPanic(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, testLogger())

	d.Panic()
	if !d.IsPanicked() {
		t.Fatal("should be panicked after Panic()")
	}

	d.Resume()
	if d.IsPanicked() {
		t.Error("should not be panicked after Resume()")
	}
}

func TestDispatcher_Panic_CallsHook(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, testLogger())

	var hookCalled atomic.Bool
	d.OnPanic = func() { hookCalled.Store(true) }

	d.Panic()
	if !hookCalled.Load() {
		t.Error("OnPanic hook was not called")
	}
}

func TestDispatcher_Resume_CallsHook(t *testing.T) {
	d := NewDispatcher(nil, nil, nil, testLogger())

	var hookCalled atomic.Bool
	d.OnResume = func() { hookCalled.Store(true) }

	d.Panic()
	d.Resume()
	if !hookCalled.Load() {
		t.Error("OnResume hook was not called")
	}
}

func TestDispatcher_StopCommand_SendsResponse(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)
	ma := &threadSafeMockAdapter{name: "telegram"}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
	)

	// /stop with no in-flight request.
	d.handleStopCommand(context.Background(), adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "/stop",
	})

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	if got := sent[0].Text; got != "No request in progress." {
		t.Errorf("stop response = %q", got)
	}
}

func TestDispatcher_PanicCommand_SendsResponse(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)
	ma := &threadSafeMockAdapter{name: "telegram"}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
	)

	d.handlePanicCommand(context.Background(), adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "/panic",
	})

	if !d.IsPanicked() {
		t.Error("should be panicked after /panic")
	}

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	if got := sent[0].Text; !contains(got, "stopped") {
		t.Errorf("panic response should mention stopped, got: %q", got)
	}
}

func TestDispatcher_ResumeCommand_SendsResponse(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)
	ma := &threadSafeMockAdapter{name: "telegram"}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
	)

	d.Panic()
	d.handleResumeCommand(context.Background(), adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "/resume",
	})

	if d.IsPanicked() {
		t.Error("should not be panicked after /resume")
	}

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 message, got %d", len(sent))
	}
	if got := sent[0].Text; got != "Processing resumed." {
		t.Errorf("resume response = %q", got)
	}
}

// --------------------------------------------------------------------------
// Ephemeral channel tests
// --------------------------------------------------------------------------

func TestChannel_IsEphemeral(t *testing.T) {
	if ch := (&Channel{SessionMode: "ephemeral"}); !ch.IsEphemeral() {
		t.Error("expected IsEphemeral() = true for ephemeral mode")
	}
	if ch := (&Channel{SessionMode: "persistent"}); ch.IsEphemeral() {
		t.Error("expected IsEphemeral() = false for persistent mode")
	}
	if ch := (&Channel{}); ch.IsEphemeral() {
		t.Error("expected IsEphemeral() = false for empty mode")
	}
}

func TestChannel_EphemeralConversationID_Format(t *testing.T) {
	ch := &Channel{Name: "scratch", SessionMode: "ephemeral"}
	id := ch.EphemeralConversationID()

	if !strings.HasPrefix(id, "chan:scratch:") {
		t.Errorf("EphemeralConversationID() = %q, want prefix chan:scratch:", id)
	}
}

func TestChannel_EphemeralConversationID_Unique(t *testing.T) {
	ch := &Channel{Name: "scratch", SessionMode: "ephemeral"}
	id1 := ch.EphemeralConversationID()
	id2 := ch.EphemeralConversationID()

	if id1 == id2 {
		t.Errorf("expected unique IDs, got %q twice", id1)
	}
}

func TestDispatcher_EphemeralChannel_UniqueConversationIDs(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	channels := []*Channel{
		{Name: "scratch", AgentName: "default", Adapters: []string{"telegram"}, SessionMode: "ephemeral"},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		nil,
		testLogger(),
		WithChannels(channels, nil),
	)

	msg1 := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "First message",
		Timestamp:  time.Now(),
	}
	msg2 := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "Second message",
		Timestamp:  time.Now(),
	}

	ch1, _ := d.resolveChannel(msg1)
	ch2, _ := d.resolveChannel(msg2)

	if ch1 == nil || ch2 == nil {
		t.Fatal("resolveChannel returned nil for ephemeral channel")
	}

	// Simulate what dispatchMessage does: assign conversation IDs.
	if ch1.IsEphemeral() {
		msg1.ConversationID = ch1.EphemeralConversationID()
	}
	if ch2.IsEphemeral() {
		msg2.ConversationID = ch2.EphemeralConversationID()
	}

	if msg1.ConversationID == msg2.ConversationID {
		t.Errorf("expected unique conversation IDs for ephemeral channel, got %q twice", msg1.ConversationID)
	}
	if !strings.HasPrefix(msg1.ConversationID, "chan:scratch:") {
		t.Errorf("conversation ID %q does not have expected prefix", msg1.ConversationID)
	}
}

func TestDispatcher_PersistentChannel_SameConversationID(t *testing.T) {
	sentDefault := &sentMessages{}
	defaultEngine := newTestEngine(t, "default", sentDefault)

	channels := []*Channel{
		{Name: "work", AgentName: "default", Adapters: []string{"telegram"}},
	}

	d := NewDispatcher(
		map[string]*Engine{"default": defaultEngine},
		nil,
		nil,
		testLogger(),
		WithChannels(channels, nil),
	)

	msg1 := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "First message",
		Timestamp:  time.Now(),
	}
	msg2 := adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		Text:       "Second message",
		Timestamp:  time.Now(),
	}

	ch1, _ := d.resolveChannel(msg1)
	ch2, _ := d.resolveChannel(msg2)

	id1 := ch1.ConversationID()
	id2 := ch2.ConversationID()

	if id1 != id2 {
		t.Errorf("expected same conversation ID for persistent channel, got %q and %q", id1, id2)
	}
	if id1 != "chan:work" {
		t.Errorf("expected conversation ID chan:work, got %q", id1)
	}
}

// ---------------------------------------------------------------------------
// End-to-end activity log pipeline tests — Dispatcher.buildEventHandler
// drives the activity log via the MessageEditor-capable mock adapter.
// ---------------------------------------------------------------------------

// newPipelineDispatcher builds a minimal dispatcher with a single
// editorMockAdapter wired up so buildEventHandler can be exercised.
// Engine wiring is not needed because we drive ChatEvents directly.
func newPipelineDispatcher(t *testing.T, ma *editorMockAdapter) *Dispatcher {
	t.Helper()
	return NewDispatcher(
		map[string]*Engine{},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
	)
}

func TestDispatcher_Pipeline_ApprovalRendersInActivityLog(t *testing.T) {
	// A pending tool_approval ChatEvent must be appended to the activity log
	// message (with inline keyboard buttons) instead of producing a
	// standalone approval message.
	ma := &editorMockAdapter{name: "telegram"}
	d := newPipelineDispatcher(t, ma)

	incoming := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "12345"}
	handle := d.buildEventHandler(context.Background(), incoming)
	if handle == nil {
		t.Fatal("buildEventHandler returned nil")
	}

	handle(ChatEvent{
		Type:             "tool_approval",
		Tool:             "web_search",
		Text:             `{"query":"test"}`,
		ApprovalCallback: "cb-1",
	})

	sent := ma.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sent))
	}
	if sent[0].ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", sent[0].ParseMode)
	}
	if len(sent[0].Buttons) != 4 {
		t.Errorf("expected 4 approval buttons, got %d", len(sent[0].Buttons))
	}
	if !strings.Contains(sent[0].Text, "🔧 <b>web_search</b> — approve?") {
		t.Errorf("missing approval header: %s", sent[0].Text)
	}
	// No standalone Send-with-buttons calls on the bare adapter — buttons
	// must travel via the MessageEditor edit path so the message is editable.
	standaloneApprovals := 0
	for _, m := range sent {
		if len(m.Buttons) > 0 && !strings.HasPrefix(m.Text, "🔧 <b>") {
			standaloneApprovals++
		}
	}
	if standaloneApprovals > 0 {
		t.Errorf("expected approvals to flow through alog, got %d standalone", standaloneApprovals)
	}
}

func TestDispatcher_Pipeline_ApproveFlow_EditsSameMessage(t *testing.T) {
	// Approve flow: tool_approval (pending) → tool_start → tool_end. All
	// three events must edit the same activity log message; only one
	// SendAndGetID call should ever happen.
	ma := &editorMockAdapter{name: "telegram"}
	d := newPipelineDispatcher(t, ma)

	incoming := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "12345"}
	handle := d.buildEventHandler(context.Background(), incoming)

	handle(ChatEvent{Type: "tool_approval", Tool: "web_search", Text: "{}", ApprovalCallback: "cb-1"})
	handle(ChatEvent{Type: "tool_start", Tool: "web_search"})
	handle(ChatEvent{Type: "tool_end", Tool: "web_search", Duration: 250})

	if got := len(ma.Sent()); got != 1 {
		t.Errorf("expected 1 SendAndGetID, got %d (the alog must edit, not spawn new messages)", got)
	}
	edits := ma.Edits()
	if len(edits) != 2 {
		t.Fatalf("expected 2 edits (start, end), got %d", len(edits))
	}

	last := edits[len(edits)-1]
	if !strings.Contains(last.Text, "🔧 <b>web_search</b> — ✅ 250ms") {
		t.Errorf("final edit missing completed line: %s", last.Text)
	}
	if len(last.Buttons) != 0 {
		t.Errorf("buttons should be removed from completed message, got %d", len(last.Buttons))
	}
	if !strings.Contains(last.Text, "<blockquote expandable>") {
		t.Errorf("completed activity log must wrap entries in blockquote: %s", last.Text)
	}
}

func TestDispatcher_Pipeline_DenyFlow_TransitionsLineInPlace(t *testing.T) {
	// Deny flow: tool_approval (pending) → tool_approval (denied). The
	// second event must transition the activity log to a denied state in
	// place, removing buttons.
	ma := &editorMockAdapter{name: "telegram"}
	d := newPipelineDispatcher(t, ma)

	incoming := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "12345"}
	handle := d.buildEventHandler(context.Background(), incoming)

	handle(ChatEvent{Type: "tool_approval", Tool: "web_search", Text: "{}", ApprovalCallback: "cb-1"})
	handle(ChatEvent{Type: "tool_approval", Tool: "web_search", ApprovalStatus: "denied"})

	if got := len(ma.Sent()); got != 1 {
		t.Errorf("expected 1 SendAndGetID, got %d", got)
	}
	edits := ma.Edits()
	if len(edits) != 1 {
		t.Fatalf("expected 1 edit (denial), got %d", len(edits))
	}
	last := edits[len(edits)-1]
	if !strings.Contains(last.Text, "🔧 <b>web_search</b> — ❌ denied") {
		t.Errorf("denial edit missing denied line: %s", last.Text)
	}
	if len(last.Buttons) != 0 {
		t.Errorf("buttons should be removed after denial, got %d", len(last.Buttons))
	}
	if strings.Contains(last.Text, "approve?") {
		t.Errorf("approval prompt should be cleared after denial: %s", last.Text)
	}
}

func TestDispatcher_Pipeline_SupervisorEscalatedRendersInActivityLog(t *testing.T) {
	// supervisor_escalated must surface a visible line in the activity log so
	// the user understands why the subsequent human-approval prompt appeared.
	// In compact (non-debug) mode the previous code took a silent return path,
	// leaving the user with no feedback about the supervisor's decision until
	// the awaitToolApproval prompt landed.
	ma := &editorMockAdapter{name: "telegram"}
	d := newPipelineDispatcher(t, ma)

	incoming := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "12345"}
	handle := d.buildEventHandler(context.Background(), incoming)

	handle(ChatEvent{Type: "tool_approval", Tool: "web_search", ApprovalStatus: "supervisor_escalated"})

	if got := len(ma.Sent()); got != 1 {
		t.Fatalf("expected 1 SendAndGetID for the alog message, got %d", got)
	}
	rendered := ma.Sent()[0].Text
	if !strings.Contains(rendered, "🔧 <b>web_search (supervisor)</b> — ↑ escalated") {
		t.Errorf("missing escalation line in activity log: %s", rendered)
	}
	for _, m := range ma.Sent() {
		if len(m.Buttons) > 0 {
			t.Errorf("escalation line should not carry buttons; the human-approval prompt arrives as a separate event")
		}
	}
}

func TestDispatcher_Pipeline_AutoApprovedAccumulatesInActivityLog(t *testing.T) {
	// Several auto-approved tools should accumulate as lines in a single
	// activity log message, not produce separate messages.
	ma := &editorMockAdapter{name: "telegram"}
	d := newPipelineDispatcher(t, ma)

	incoming := adapter.IncomingMessage{Adapter: "telegram", ExternalID: "12345"}
	handle := d.buildEventHandler(context.Background(), incoming)

	for _, tool := range []string{"tool_a", "tool_b", "tool_c"} {
		handle(ChatEvent{Type: "tool_approval", Tool: tool, ApprovalStatus: "auto_approved"})
	}

	if got := len(ma.Sent()); got != 1 {
		t.Errorf("expected 1 SendAndGetID across 3 events, got %d", got)
	}
	edits := ma.Edits()
	if len(edits) != 2 {
		t.Errorf("expected 2 edits (events 2 and 3), got %d", len(edits))
	}
	last := edits[len(edits)-1]
	for _, tool := range []string{"tool_a", "tool_b", "tool_c"} {
		if !strings.Contains(last.Text, "🔧 <b>"+tool+"</b> — auto-approved") {
			t.Errorf("missing line for %s in: %s", tool, last.Text)
		}
	}
}

// ---------------------------------------------------------------------------
// Approval surfacing tests — approvals must never be silently dropped
// ---------------------------------------------------------------------------

func TestDispatcher_Dispatch_WiresEventHandler_ApprovalsReachAdapter(t *testing.T) {
	// Dispatch() must wire up an event handler so that tool approval dialogs
	// are sent to the adapter. This tests the fix for the bug where scheduled
	// messages used HandleMessage (nil onEvent) causing approvals to silently
	// time out without surfacing to a human.

	store, err := NewSQLiteMemoryStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

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
				Content:      "Here are the results.",
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
	t.Cleanup(func() { _ = approvalStore.Close() })
	mgr := approval.NewManager(approvalStore, testLogger())

	sent := &sentMessages{}
	permissions, _ := security.NewPermissionEngine("supervised")

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "Test assistant.", nil, toolMgr, mgr, testLogger())

	// Register a mock adapter so buildEventHandler can find it.
	ma := &threadSafeMockAdapter{name: "telegram"}

	d := NewDispatcher(
		map[string]*Engine{"default": engine},
		nil,
		[]adapter.Adapter{ma},
		testLogger(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Dispatch(ctx, "default", adapter.IncomingMessage{
			Adapter:    "telegram",
			ExternalID: "12345",
			UserID:     "user-1",
			UserName:   "scheduler",
			Text:       "search for test",
			Timestamp:  time.Now(),
		})
	}()

	// Wait for the approval to be submitted, then approve it.
	time.Sleep(200 * time.Millisecond)
	approvals, _ := mgr.List(ctx, approval.StatusPending)
	if len(approvals) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(approvals))
	}
	_, _ = mgr.Resolve(ctx, approvals[0].ID, true, "test-operator")

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Dispatch: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for Dispatch to complete")
	}

	// The adapter must have received the approval dialog as a message with buttons.
	adapterSent := ma.Sent()
	var approvalMsg *adapter.OutgoingMessage
	for i := range adapterSent {
		if len(adapterSent[i].Buttons) > 0 {
			approvalMsg = &adapterSent[i]
			break
		}
	}
	if approvalMsg == nil {
		t.Fatal("adapter did not receive an approval dialog with buttons — approvals would silently time out")
	}
	if approvalMsg.ExternalID != "12345" {
		t.Errorf("approval dialog sent to wrong chat: got %q, want 12345", approvalMsg.ExternalID)
	}
	// Should have approve/deny buttons at minimum.
	if len(approvalMsg.Buttons) < 2 {
		t.Errorf("approval dialog has %d buttons, want at least 2 (approve/deny)", len(approvalMsg.Buttons))
	}
}

func TestDispatcher_Dispatch_NoAdapter_ApprovalDeniedImmediately(t *testing.T) {
	// When Dispatch() is called with an adapter that is NOT registered in
	// the dispatcher, buildEventHandler returns a no-op. The engine should
	// detect that onEvent cannot surface approvals and deny immediately.
	// This test verifies the denial is fast (not a 5-minute timeout).

	store, err := NewSQLiteMemoryStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

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
				Content:      "Tool was denied.",
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
	t.Cleanup(func() { _ = approvalStore.Close() })
	mgr := approval.NewManager(approvalStore, testLogger())

	sent := &sentMessages{}
	permissions, _ := security.NewPermissionEngine("supervised")

	toolMgr := tool.NewManager(testLogger())
	engine := NewEngine("default", router, store, sent.send, permissions, nil, "Test assistant.", nil, toolMgr, mgr, testLogger())
	// Long timeout to prove we DON'T wait for it.
	engine.SetApprovalConfig(10*time.Second, 0)

	// NO adapters registered — buildEventHandler returns a no-op.
	d := NewDispatcher(
		map[string]*Engine{"default": engine},
		nil,
		nil, // no adapters
		testLogger(),
	)

	start := time.Now()
	err = d.Dispatch(context.Background(), "default", adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: "12345",
		UserID:     "user-1",
		UserName:   "scheduler",
		Text:       "search for test",
		Timestamp:  time.Now(),
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// Must complete much faster than the 10s approval timeout.
	if elapsed > 3*time.Second {
		t.Errorf("Dispatch took %v — approval was not denied immediately (timeout is 10s)", elapsed)
	}

	// The LLM should have received a response (the denial fed back, then second LLM response).
	if len(sent.msgs) < 1 {
		t.Fatal("expected at least 1 sent message")
	}
}
