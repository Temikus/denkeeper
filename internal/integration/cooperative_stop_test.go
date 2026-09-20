//go:build integration

// End-to-end coverage for the cooperative stop on the three user-facing cancel
// paths — POST /sessions/{id}/stop and the WebSocket cancel frame here, /stop in
// internal/agent. Stages 3 and 4 of design/plans/6-step-boundary-stop.md: a
// cancel now ends the turn at its next step boundary with a persisted reply,
// instead of killing the context mid-step and answering nothing.
package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

const cancelMarker = "[engine: turn ended early — cancelled at your request]"

// stopHarness wires the echo tool into an agent of the given tier, so a turn
// can be caught inside a tool loop (or inside an approval wait).
func stopHarness(t *testing.T, tier string, responder func(req llm.ChatRequest) (*llm.ChatResponse, error)) *Harness {
	t.Helper()

	ts := startTestMCPServer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolMgr := tool.NewManager(logger)
	if err := toolMgr.RegisterServer(context.Background(), "echo-tool", config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}); err != nil {
		t.Fatalf("registering test MCP server: %v", err)
	}
	t.Cleanup(func() { _ = toolMgr.Close() })

	return NewHarness(t, &HarnessOpts{
		Agents:      []agentSetup{{Name: "default", Tier: tier}},
		ToolManager: toolMgr,
		Responder:   responder,
	})
}

func echoToolCallResponse(id string) *llm.ChatResponse {
	return &llm.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{
			{ID: id, Type: "function", Function: llm.FunctionCall{
				Name: "echo", Arguments: `{"input":"step"}`,
			}},
		},
		TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
		Model:      "test-model",
	}
}

// loopResponder answers a two-round tool loop, parking the round-1 follow-up
// completion until the test has issued its stop. That makes the stop land
// exactly at a round boundary with one tool call already committed.
func loopResponder(atBoundary chan<- struct{}, resume <-chan struct{}) func(llm.ChatRequest) (*llm.ChatResponse, error) {
	var calls atomic.Int64
	return func(_ llm.ChatRequest) (*llm.ChatResponse, error) {
		switch calls.Add(1) {
		case 1:
			return echoToolCallResponse("call_1"), nil
		case 2:
			close(atBoundary)
			<-resume
			// The model asks for another round; the engine must refuse to start it.
			return echoToolCallResponse("call_2"), nil
		default:
			return &llm.ChatResponse{
				Content:      "I echoed once before you stopped me.",
				FinishReason: "stop",
				TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
				Model:        "test-model",
			}, nil
		}
	}
}

// A stop issued mid-tool-loop must produce a completed turn: the reply carries
// the wrap-up plus the cancel marker, and it is persisted as an assistant
// message rather than leaving the user message dangling.
func TestStopSession_MidToolLoop_CompletesTurnWithReply(t *testing.T) {
	atBoundary := make(chan struct{})
	resume := make(chan struct{})
	h := stopHarness(t, "autonomous", loopResponder(atBoundary, resume))

	sessionID := "stop-mid-loop"
	type chatResult struct {
		code int
		body string
	}
	done := make(chan chatResult, 1)
	go func() {
		rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
			"message":    "do the thing",
			"session_id": sessionID,
		}))
		done <- chatResult{rec.Code, rec.Body.String()}
	}()

	select {
	case <-atBoundary:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached its round boundary")
	}

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/sessions/"+sessionID+"/stop", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stop status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	close(resume)

	var result chatResult
	select {
	case result = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the chat request never returned — a cooperative stop must still answer")
	}
	if result.code != http.StatusOK {
		t.Fatalf("chat status = %d, want 200; body: %s", result.code, result.body)
	}
	if !strings.Contains(result.body, "cancelled at your request") {
		t.Errorf("response = %s, want the cancel marker", result.body)
	}
	if !strings.Contains(result.body, "I echoed once before you stopped me.") {
		t.Errorf("response = %s, want the wrap-up text", result.body)
	}

	msgs, err := h.Memory.GetMessages(context.Background(), sessionID, 10)
	if err != nil {
		t.Fatalf("reading messages: %v", err)
	}
	var assistant int
	for _, m := range msgs {
		if m.Role == "assistant" {
			assistant++
			if !strings.Contains(m.Content, cancelMarker) {
				t.Errorf("stored assistant content = %q, want the cancel marker", m.Content)
			}
		}
	}
	if assistant != 1 {
		t.Errorf("stored %d assistant messages, want 1 (a stopped turn must not leave a dangling user message)", assistant)
	}
}

// Stage 4: a stop while the engine is blocked on an approval must end the wait
// and resolve the request as aborted, rather than orphaning a pending row.
func TestStopSession_WhileApprovalPending_AbortsApproval(t *testing.T) {
	h := stopHarness(t, "supervised", func(_ llm.ChatRequest) (*llm.ChatResponse, error) {
		return echoToolCallResponse("call_1"), nil
	})

	sessionID := "stop-pending-approval"
	done := make(chan string, 1)
	go func() {
		// SSE, because an approval can only be raised on a turn with an event
		// handler to surface it — a plain JSON chat denies the call outright.
		req := h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
			"message":    "do the thing",
			"session_id": sessionID,
		})
		req.Header.Set("Accept", "text/event-stream")
		done <- h.Do(req).Body.String()
	}()

	approvalID := waitForPendingApproval(t, h)

	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/sessions/"+sessionID+"/stop", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stop status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	select {
	case body := <-done:
		if !strings.Contains(body, "cancelled at your request") {
			t.Errorf("response = %s, want the cancel marker", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the chat request never returned — the approval wait did not observe the stop")
	}

	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/approvals/"+approvalID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get approval status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var appr map[string]any
	DecodeJSON(t, rec, &appr)
	if appr["status"] != "aborted" {
		t.Errorf("approval status = %v, want aborted (not pending, and not a denial nobody made)", appr["status"])
	}
}

// waitForPendingApproval polls until the engine has submitted its approval and
// returns its id.
func waitForPendingApproval(t *testing.T, h *Harness) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/approvals?status=pending", nil))
		if rec.Code == http.StatusOK {
			var pending []map[string]any
			_ = json.NewDecoder(rec.Body).Decode(&pending)
			if len(pending) > 0 {
				if id, _ := pending[0]["id"].(string); id != "" {
					return id
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no approval became pending")
	return ""
}

// A WebSocket cancel frame takes the same cooperative path, so the client that
// pressed stop still receives content and done frames for the turn it stopped.
func TestWebSocketCancel_CompletesTurnWithReply(t *testing.T) {
	atBoundary := make(chan struct{})
	resume := make(chan struct{})
	h := stopHarness(t, "autonomous", loopResponder(atBoundary, resume))

	ts := httptest.NewServer(h.Handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws?token=" + h.APIKey
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	sessionID := "ws-cancel-session"
	if err := conn.WriteJSON(map[string]any{
		"type":       "chat_request",
		"message":    "do the thing",
		"session_id": sessionID,
	}); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	select {
	case <-atBoundary:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached its round boundary")
	}

	if err := conn.WriteJSON(map[string]any{"type": "cancel", "session_id": sessionID}); err != nil {
		t.Fatalf("ws cancel write: %v", err)
	}
	// A write only proves the frame was sent, and the turn must not be released
	// until the server has acted on it. The read pump handles frames in order
	// and answers an unknown one with an invalid_frame error, so that error is
	// the barrier saying the cancel ahead of it has been processed.
	if err := conn.WriteJSON(map[string]any{"type": "not-a-frame-type"}); err != nil {
		t.Fatalf("ws barrier write: %v", err)
	}

	var content string
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("ws read: %v (content so far: %q)", err, content)
		}
		var frame map[string]any
		if err := json.Unmarshal(msg, &frame); err != nil {
			t.Fatalf("ws unmarshal: %v; raw: %s", err, msg)
		}
		switch {
		case frame["type"] == "error" && frame["code"] == "invalid_frame":
			close(resume)
			continue
		case frame["type"] == "error":
			t.Fatalf("cancel produced an error frame instead of a reply: %s", msg)
		case frame["type"] == "content":
			content, _ = frame["text"].(string)
		}
		if frame["type"] == "done" {
			break
		}
	}

	if !strings.Contains(content, cancelMarker) {
		t.Errorf("content frame = %q, want the cancel marker", content)
	}
}
