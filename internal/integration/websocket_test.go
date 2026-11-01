//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Temikus/denkeeper/internal/approval"
)

func TestWebSocket_UpgradeAndChat(t *testing.T) {
	h := NewHarness(t, nil)

	// Start a real HTTP test server (needed for WebSocket upgrade).
	ts := httptest.NewServer(h.Handler)
	defer ts.Close()

	// Build WebSocket URL.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws?token=" + h.APIKey

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	// Send a chat request.
	chatReq := map[string]any{
		"type":    "chat_request",
		"message": "hello from ws",
	}
	if err := conn.WriteJSON(chatReq); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	// Read frames until we get a "done" event (or timeout).
	var events []map[string]any
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ws read: %v (events so far: %d)", err, len(events))
		}

		var frame map[string]any
		if err := json.Unmarshal(msg, &frame); err != nil {
			t.Fatalf("ws unmarshal: %v; raw: %s", err, string(msg))
		}
		events = append(events, frame)

		if frame["type"] == "done" {
			break
		}
	}

	// We should have received at least: thinking, usage, content, done.
	if len(events) < 3 {
		t.Fatalf("events count = %d, want >= 3; events: %v", len(events), events)
	}

	// Verify we got a content event with the mock response.
	foundContent := false
	for _, ev := range events {
		if ev["type"] == "content" {
			if ev["text"] == "Hello from mock!" {
				foundContent = true
			}
			break
		}
	}
	if !foundContent {
		t.Error("no content event with expected text found")
	}

	// Verify the done event has a session_id.
	lastEvent := events[len(events)-1]
	if lastEvent["type"] != "done" {
		t.Errorf("last event type = %v, want done", lastEvent["type"])
	}
	if lastEvent["session_id"] == nil || lastEvent["session_id"] == "" {
		t.Error("done event should have session_id")
	}
}

func TestWebSocket_AuthRequired(t *testing.T) {
	h := NewHarness(t, nil)

	ts := httptest.NewServer(h.Handler)
	defer ts.Close()

	// Try connecting without a token.
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws"

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected ws dial to fail without auth")
	}
	if resp != nil && resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestWebSocket_InsufficientScope_Returns401(t *testing.T) {
	// WebSocket upgrade always returns 401 (not 403) even when the key is valid
	// but lacks the required scope. WS clients cannot act on a 403 differently.
	h := NewHarness(t, &HarnessOpts{
		Scopes: []string{"skills:read"}, // valid key, but no "chat" scope needed for WS
	})

	ts := httptest.NewServer(h.Handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws?token=" + h.APIKey

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected ws dial to fail with insufficient scope")
	}
	if resp != nil && resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401 (not 403 — WS always uses 401)", resp.StatusCode)
	}
}

func TestWebSocket_SessionPersistence(t *testing.T) {
	h := NewHarness(t, nil)

	ts := httptest.NewServer(h.Handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws?token=" + h.APIKey

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	// First message.
	if err := conn.WriteJSON(map[string]any{
		"type":       "chat_request",
		"message":    "first message",
		"session_id": "ws-persist-test",
	}); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	var firstSessionID string
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		var frame map[string]any
		json.Unmarshal(msg, &frame)
		if frame["type"] == "done" {
			if sid, ok := frame["session_id"].(string); ok {
				firstSessionID = sid
			}
			break
		}
	}

	if firstSessionID != "ws-persist-test" {
		t.Errorf("session_id = %v, want ws-persist-test", firstSessionID)
	}

	// Second message in same session.
	if err := conn.WriteJSON(map[string]any{
		"type":       "chat_request",
		"message":    "second message",
		"session_id": "ws-persist-test",
	}); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		var frame map[string]any
		json.Unmarshal(msg, &frame)
		if frame["type"] == "done" {
			break
		}
	}

	// Verify history was passed to the LLM on the second call.
	lastReq := h.MockLLM.LastRequest()
	if len(lastReq.Messages) < 3 {
		t.Errorf("second request had %d messages, want >= 3 (should include history)", len(lastReq.Messages))
	}
}

// wsDialKey opens a WebSocket on a fresh test server using the harness key.
func wsDialKey(t *testing.T, h *Harness) *websocket.Conn {
	t.Helper()
	return wsDialToken(t, h, h.APIKey)
}

func wsDialToken(t *testing.T, h *Harness, token string) *websocket.Conn {
	t.Helper()
	ts := httptest.NewServer(h.Handler)
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// wsReadError reads frames until an error frame arrives and returns its code.
func wsReadError(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var frame map[string]any
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("ws read: %v", err)
		}
		if frame["type"] == "error" {
			code, _ := frame["code"].(string)
			return code
		}
	}
}

func submitTestApproval(t *testing.T, h *Harness) *approval.Request {
	t.Helper()
	req, err := h.Approvals.Submit(context.Background(), "default", approval.ActionKindToolCall,
		`Execute tool "kv_set" with args: {}`, "payload", "chat-1", "api", "session-1",
		func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return req
}

func TestWebSocket_ChatScopeCannotResolveApprovals(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{Scopes: []string{"chat"}})
	conn := wsDialKey(t, h)
	req := submitTestApproval(t, h)

	if err := conn.WriteJSON(map[string]any{
		"type": "approval_response", "approval_id": req.ID, "action": "auto_always",
	}); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	if code := wsReadError(t, conn); code != "forbidden" {
		t.Fatalf("error code = %q, want forbidden", code)
	}

	got, err := h.Approvals.Get(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != approval.StatusPending {
		t.Errorf("status = %q, want pending", got.Status)
	}
	rules, err := h.Approvals.ListAutoApproveRules(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListAutoApproveRules: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("auto-approve rules = %d, want 0", len(rules))
	}
}

func TestWebSocket_ApprovalsWriteScopeResolvesApprovals(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{Scopes: []string{"chat", "approvals:write"}})
	conn := wsDialKey(t, h)
	req := submitTestApproval(t, h)

	if err := conn.WriteJSON(map[string]any{
		"type": "approval_response", "approval_id": req.ID, "action": "approve",
	}); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	// Frames are handled in order, so a repeat is answered only after the
	// first approve has resolved the request.
	if err := conn.WriteJSON(map[string]any{
		"type": "approval_response", "approval_id": req.ID, "action": "approve",
	}); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	if code := wsReadError(t, conn); code != "already_resolved" {
		t.Fatalf("error code = %q, want already_resolved", code)
	}
}

func TestWebSocket_ChatScopeCannotPanic(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{Scopes: []string{"chat", "approvals:write"}})
	conn := wsDialKey(t, h)

	if err := conn.WriteJSON(map[string]any{"type": "panic"}); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	if code := wsReadError(t, conn); code != "forbidden" {
		t.Fatalf("error code = %q, want forbidden", code)
	}
	if h.Dispatcher.IsPanicked() {
		t.Error("dispatcher panicked from a non-admin WS connection")
	}
}

func TestWebSocket_RevokedKeyLosesApprovalPower(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{WithKeyStore: true})
	id, key := createTestKey(t, h, "ws-approver", []string{"chat", "approvals:write"})
	conn := wsDialToken(t, h, key)
	req := submitTestApproval(t, h)

	rec := h.Do(h.AuthedRequest(http.MethodDelete, "/api/v1/keys/"+id, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if err := conn.WriteJSON(map[string]any{
		"type": "approval_response", "approval_id": req.ID, "action": "approve",
	}); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	if code := wsReadError(t, conn); code != "forbidden" {
		t.Fatalf("error code = %q, want forbidden", code)
	}
	got, err := h.Approvals.Get(context.Background(), req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != approval.StatusPending {
		t.Errorf("status = %q, want pending", got.Status)
	}
}
