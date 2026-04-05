package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
