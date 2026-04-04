//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

func TestChat_JSONResponse(t *testing.T) {
	h := NewHarness(t, nil)

	req := h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "hello",
	})
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["response"] != "Hello from mock!" {
		t.Errorf("response = %v, want 'Hello from mock!'", resp["response"])
	}
	if resp["session_id"] == nil || resp["session_id"] == "" {
		t.Error("session_id should be generated and returned")
	}
}

func TestChat_ExplicitSessionID(t *testing.T) {
	h := NewHarness(t, nil)

	req := h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message":    "hello",
		"session_id": "my-session-42",
	})
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	DecodeJSON(t, rec, &resp)
	if resp["session_id"] != "my-session-42" {
		t.Errorf("session_id = %v, want my-session-42", resp["session_id"])
	}
}

func TestChat_SSEStreaming(t *testing.T) {
	h := NewHarness(t, nil)

	req := h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "hello",
	})
	req.Header.Set("Accept", "text/event-stream")
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Parse SSE events.
	var events []map[string]any
	scanner := bufio.NewScanner(rec.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("parse SSE event: %v", err)
		}
		events = append(events, ev)
	}

	// Events: thinking, usage, content, done
	if len(events) != 4 {
		t.Fatalf("events count = %d, want 4; events: %v", len(events), events)
	}
	if events[0]["type"] != "thinking" {
		t.Errorf("events[0] = %v, want thinking", events[0])
	}
	if events[1]["type"] != "usage" {
		t.Errorf("events[1] = %v, want usage", events[1])
	}
	if events[2]["type"] != "content" || events[2]["text"] != "Hello from mock!" {
		t.Errorf("events[2] = %v, want content/Hello from mock!", events[2])
	}
	if events[3]["type"] != "done" || events[3]["session_id"] == "" {
		t.Errorf("events[3] = %v, want done with session_id", events[3])
	}
}

func TestChat_SessionPersistence(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{
			{Content: "first reply", TokensUsed: llm.TokenUsage{Total: 10}, Model: "test-model", FinishReason: "stop"},
			{Content: "second reply", TokensUsed: llm.TokenUsage{Total: 10}, Model: "test-model", FinishReason: "stop"},
		},
	})

	sessionID := "persist-session"

	// First message.
	rec1 := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message":    "first",
		"session_id": sessionID,
	}))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first status = %d", rec1.Code)
	}

	// Second message in same session.
	rec2 := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message":    "second",
		"session_id": sessionID,
	}))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second status = %d", rec2.Code)
	}

	// Verify 4 messages stored: 2 user + 2 assistant.
	ctx := context.Background()
	messages, err := h.Memory.GetMessages(ctx, sessionID, 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(messages) != 4 {
		t.Errorf("stored %d messages, want 4", len(messages))
	}

	// Second call should have received conversation history.
	lastReq := h.MockLLM.LastRequest()
	// The last request should include prior messages (system + user + assistant + user).
	if len(lastReq.Messages) < 3 {
		t.Errorf("second request had %d messages, want >= 3 (history should be included)", len(lastReq.Messages))
	}
}

func TestChat_EmptyMessage(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "",
	}))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestChat_UnknownAgent(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message": "hello",
		"agent":   "nonexistent",
	}))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
