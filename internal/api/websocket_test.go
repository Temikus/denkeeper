package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
)

// wsTestServer creates an httptest.Server with a WebSocket-enabled Server.
func wsTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	cfg := testConfig(allScopesKey())
	cfg.WebSocketEnabled = boolPtr(true)
	cfg.WebSocketMaxConnections = 10
	cfg.WebSocketReplayBufferTTL = "5m"
	srv := New(cfg, testDeps(), testLogger())
	ts := httptest.NewServer(srv.httpServer.Handler)
	t.Cleanup(ts.Close)
	return ts, srv
}

// wsURL converts the test server URL from http to ws and appends the path + auth.
func wsURL(ts *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + path + "?token=dk-test-key"
}

// dialWS connects to the test server's WS endpoint.
func dialWS(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(ts, "/api/v1/ws"), nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v (resp status: %v)", err, resp)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readFrame reads and parses a JSON frame from the WS connection.
func readFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var frame map[string]any
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("unmarshal frame: %v; data=%s", err, data)
	}
	return frame
}

func TestWSChat_EndToEnd(t *testing.T) {
	ts, _ := wsTestServer(t)
	conn := dialWS(t, ts)

	// Send a chat request.
	msg := ChatRequestFrame{
		Type:    FrameTypeChatRequest,
		Agent:   "default",
		Message: "hello",
	}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Collect frames until we see "done".
	var types []string
	var sessionID string
	var lastSeq float64
	for {
		frame := readFrame(t, conn)
		typ, _ := frame["type"].(string)
		types = append(types, typ)
		if seq, ok := frame["seq"].(float64); ok && seq > lastSeq {
			lastSeq = seq
		}
		if sid, ok := frame["session_id"].(string); ok && sid != "" {
			sessionID = sid
		}
		if typ == "done" {
			break
		}
		if typ == "error" {
			t.Fatalf("unexpected error frame: %v", frame)
		}
	}

	// Verify we got expected event types.
	if len(types) < 3 {
		t.Errorf("got %d frames, want at least 3 (thinking + content + done)", len(types))
	}
	if types[len(types)-2] != "content" {
		t.Errorf("second-to-last frame type = %q, want content", types[len(types)-2])
	}
	if types[len(types)-1] != "done" {
		t.Errorf("last frame type = %q, want done", types[len(types)-1])
	}

	// Verify session ID was assigned.
	if sessionID == "" {
		t.Error("expected non-empty session_id in frames")
	}

	// Verify sequence numbers are monotonically increasing.
	if lastSeq < 2 {
		t.Errorf("last seq = %v, want >= 2", lastSeq)
	}
}

func TestWSChat_AgentNotFound(t *testing.T) {
	ts, _ := wsTestServer(t)
	conn := dialWS(t, ts)

	msg := ChatRequestFrame{
		Type:    FrameTypeChatRequest,
		Agent:   "nonexistent",
		Message: "hello",
	}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	frame := readFrame(t, conn)
	if frame["type"] != "error" {
		t.Errorf("type = %q, want error", frame["type"])
	}
	if frame["code"] != "agent_not_found" {
		t.Errorf("code = %q, want agent_not_found", frame["code"])
	}
}

func TestWSChat_AuthRequired(t *testing.T) {
	cfg := testConfig(allScopesKey())
	cfg.WebSocketEnabled = boolPtr(true)
	srv := New(cfg, testDeps(), testLogger())
	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	// Connect without auth token.
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws"
	_, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected error connecting without auth")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestWSHub_MaxConnections(t *testing.T) {
	hub := NewWSHub(2, 5*time.Minute, testLogger())

	c1 := &WSConn{done: make(chan struct{})}
	c2 := &WSConn{done: make(chan struct{})}
	c3 := &WSConn{done: make(chan struct{})}

	if !hub.Register(c1) {
		t.Error("expected register c1 to succeed")
	}
	if !hub.Register(c2) {
		t.Error("expected register c2 to succeed")
	}
	if hub.Register(c3) {
		t.Error("expected register c3 to fail (at capacity)")
	}

	hub.Unregister(c1)
	if !hub.Register(c3) {
		t.Error("expected register c3 to succeed after unregister")
	}

	if hub.ConnCount() != 2 {
		t.Errorf("conn count = %d, want 2", hub.ConnCount())
	}
}

func TestWSHub_RegisterUnregister(t *testing.T) {
	hub := NewWSHub(0, 5*time.Minute, testLogger())
	c := &WSConn{done: make(chan struct{})}

	if !hub.Register(c) {
		t.Error("expected register to succeed")
	}
	if hub.ConnCount() != 1 {
		t.Errorf("conn count = %d, want 1", hub.ConnCount())
	}

	hub.Unregister(c)
	if hub.ConnCount() != 0 {
		t.Errorf("conn count = %d, want 0", hub.ConnCount())
	}

	// Unregister again is a no-op.
	hub.Unregister(c)
	if hub.ConnCount() != 0 {
		t.Errorf("conn count = %d, want 0 after double unregister", hub.ConnCount())
	}
}

func TestWSChat_ReplayBuffer(t *testing.T) {
	ts, srv := wsTestServer(t)
	conn := dialWS(t, ts)

	// Send a chat request to populate the replay buffer.
	msg := ChatRequestFrame{
		Type:    FrameTypeChatRequest,
		Agent:   "default",
		Message: "hello",
	}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Collect all frames and track session + seq.
	var sessionID string
	var seq1 float64
	for {
		frame := readFrame(t, conn)
		if sid, ok := frame["session_id"].(string); ok && sid != "" {
			sessionID = sid
		}
		if s, ok := frame["seq"].(float64); ok && s > seq1 {
			seq1 = s
		}
		if frame["type"] == "done" {
			break
		}
	}

	// The replay buffer should have entries.
	if srv.wsHub != nil {
		buf := srv.wsHub.replayStore.Buffer(sessionID)
		if buf.Len() == 0 {
			t.Error("expected replay buffer to have entries after chat")
		}
	}
}

func TestWSHealth_WSEnabled(t *testing.T) {
	cfg := testConfig(allScopesKey())
	cfg.WebSocketEnabled = boolPtr(true)
	srv := New(cfg, testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ws_enabled"] != true {
		t.Errorf("ws_enabled = %v, want true", body["ws_enabled"])
	}
}

func TestWSHealth_WSDisabled(t *testing.T) {
	cfg := testConfig(allScopesKey())
	cfg.WebSocketEnabled = boolPtr(false)
	srv := New(cfg, testDeps(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["ws_enabled"] != false {
		t.Errorf("ws_enabled = %v, want false", body["ws_enabled"])
	}
}

// ---------------------------------------------------------------------------
// blockingProvider — an LLM provider whose ChatCompletion blocks until
// its gate channel is closed, allowing tests to hold semaphore slots open.
// ---------------------------------------------------------------------------

type blockingProvider struct {
	gate     chan struct{} // close to unblock all pending calls
	entered  chan struct{} // receives a value each time a call enters
	inflight atomic.Int64  // number of currently blocked calls
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{
		gate:    make(chan struct{}),
		entered: make(chan struct{}, 100),
	}
}

func (b *blockingProvider) Name() string { return "mock" }

func (b *blockingProvider) ChatCompletion(ctx context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	b.inflight.Add(1)
	defer b.inflight.Add(-1)
	// Signal that we have entered the provider.
	select {
	case b.entered <- struct{}{}:
	default:
	}
	select {
	case <-b.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &llm.ChatResponse{
		Content:      "Hello from blocking mock!",
		TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
		Model:        "test-model",
		FinishReason: "stop",
	}, nil
}

func (b *blockingProvider) HealthCheck(_ context.Context) error { return nil }

// wsTestServerWithProvider creates a test server using the given LLM provider,
// so tests can control when ChatCompletion returns. It uses a file-based
// SQLite store (with WAL mode) instead of :memory: to support concurrent
// goroutine access without connection-pool isolation issues.
func wsTestServerWithProvider(t *testing.T, prov llm.Provider) (*httptest.Server, *Server) {
	t.Helper()
	logger := testLogger()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	mem, err := agent.NewSQLiteMemoryStore(dbPath)
	if err != nil {
		t.Fatalf("creating file-based memory store: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	costTracker := llm.NewCostTracker(llm.SessionLimits{Hard: 1.0}, nil)

	perms, _ := security.NewPermissionEngine("supervised")
	router := llm.NewRouter("mock", "test-model", costTracker)
	router.RegisterProvider(prov)

	approvalStore, _ := approval.NewInMemoryStore()
	approvalMgr := approval.NewManager(approvalStore, logger)

	e := agent.NewEngine("default", router, mem, nil, perms, nil, "test", []skill.Skill{}, nil, approvalMgr, logger)

	dispatcher := agent.NewDispatcher(
		map[string]*agent.Engine{"default": e},
		[]agent.Binding{{Pattern: "telegram", AgentName: "default"}},
		nil,
		logger,
	)

	sched := scheduler.New(logger)

	deps := Deps{
		Dispatcher:  dispatcher,
		Scheduler:   sched,
		CostTracker: costTracker,
		Memory:      mem,
		Approvals:   approvalMgr,
		Config: &config.Config{
			Agents: []config.AgentInstanceConfig{
				{Name: "default", Adapters: []string{"telegram"}},
			},
		},
	}

	cfg := testConfig(allScopesKey())
	cfg.WebSocketEnabled = boolPtr(true)
	cfg.WebSocketMaxConnections = 100
	cfg.WebSocketReplayBufferTTL = "5m"
	srv := New(cfg, deps, logger)
	ts := httptest.NewServer(srv.httpServer.Handler)
	t.Cleanup(ts.Close)
	return ts, srv
}

func TestWSConn_ConcurrentChatSemaphore(t *testing.T) {
	bp := newBlockingProvider()
	ts, _ := wsTestServerWithProvider(t, bp)
	conn := dialWS(t, ts)

	// Send wsMaxConcurrentChats (10) requests that will all block.
	for i := 0; i < wsMaxConcurrentChats; i++ {
		msg := ChatRequestFrame{
			Type:    FrameTypeChatRequest,
			Agent:   "default",
			Message: "hello",
		}
		if err := conn.WriteJSON(msg); err != nil {
			t.Fatalf("write chat %d: %v", i, err)
		}
	}

	// Wait until all 10 goroutines have entered the provider.
	deadline := time.After(10 * time.Second)
	for i := 0; i < wsMaxConcurrentChats; i++ {
		select {
		case <-bp.entered:
		case <-deadline:
			t.Fatalf("timed out waiting for goroutine %d to enter provider (inflight=%d)",
				i, bp.inflight.Load())
		}
	}

	// The 11th request should be rejected immediately with rate_limited.
	msg := ChatRequestFrame{
		Type:    FrameTypeChatRequest,
		Agent:   "default",
		Message: "one too many",
	}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("write overflow chat: %v", err)
	}

	// Read frames until we find the rate_limited error. The 10 in-flight
	// chats emit "thinking" events before the 11th gets rejected, so we
	// must skip those.
	var gotRateLimited bool
	readDeadline := time.Now().Add(5 * time.Second)
	for !gotRateLimited {
		_ = conn.SetReadDeadline(readDeadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var frame map[string]any
		_ = json.Unmarshal(data, &frame)
		if frame["type"] == "error" && frame["code"] == "rate_limited" {
			gotRateLimited = true
		}
	}

	if !gotRateLimited {
		t.Error("expected a rate_limited error frame for the 11th concurrent request")
	}

	// Unblock all goroutines so test can clean up.
	close(bp.gate)
}

func TestWSConn_SemaphoreReleasedAfterCompletion(t *testing.T) {
	bp := newBlockingProvider()
	ts, _ := wsTestServerWithProvider(t, bp)
	conn := dialWS(t, ts)

	// Fill all semaphore slots.
	for i := 0; i < wsMaxConcurrentChats; i++ {
		msg := ChatRequestFrame{
			Type:    FrameTypeChatRequest,
			Agent:   "default",
			Message: "hello",
		}
		if err := conn.WriteJSON(msg); err != nil {
			t.Fatalf("write chat %d: %v", i, err)
		}
	}

	// Wait until all slots are occupied.
	deadline := time.After(10 * time.Second)
	for i := 0; i < wsMaxConcurrentChats; i++ {
		select {
		case <-bp.entered:
		case <-deadline:
			t.Fatalf("timed out waiting for goroutine %d to enter provider", i)
		}
	}

	// Unblock all pending calls so they complete and release semaphore slots.
	close(bp.gate)

	// Drain all frames from the completed chats (content + done per chat).
	doneCount := 0
	drainDeadline := time.Now().Add(5 * time.Second)
	for doneCount < wsMaxConcurrentChats {
		_ = conn.SetReadDeadline(drainDeadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("drain read: %v (doneCount=%d)", err, doneCount)
		}
		var f map[string]any
		_ = json.Unmarshal(data, &f)
		if f["type"] == "done" {
			doneCount++
		}
	}

	// Since we already unblocked bp, the next request will complete immediately.
	msg := ChatRequestFrame{
		Type:    FrameTypeChatRequest,
		Agent:   "default",
		Message: "after release",
	}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("write follow-up: %v", err)
	}

	// Read frames — we should get content + done, not a rate_limited error.
	var gotDone bool
	readDeadline := time.Now().Add(5 * time.Second)
	for !gotDone {
		_ = conn.SetReadDeadline(readDeadline)
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("follow-up read: %v", err)
		}
		var f map[string]any
		_ = json.Unmarshal(data, &f)
		if f["type"] == "error" && f["code"] == "rate_limited" {
			t.Fatal("follow-up request was rate-limited; semaphore was not released")
		}
		if f["type"] == "done" {
			gotDone = true
		}
	}
}

func TestWSHub_ConcurrentRegisterUnregister(t *testing.T) {
	hub := NewWSHub(0, 5*time.Minute, testLogger()) // unlimited capacity

	const goroutines = 50
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				c := &WSConn{done: make(chan struct{})}
				if !hub.Register(c) {
					t.Errorf("register failed unexpectedly")
					return
				}
				// Immediately unregister to keep the map from growing unbounded.
				hub.Unregister(c)
			}
		}()
	}

	wg.Wait()

	// After all goroutines complete, every connection was unregistered.
	if count := hub.ConnCount(); count != 0 {
		t.Errorf("final conn count = %d, want 0", count)
	}
}
