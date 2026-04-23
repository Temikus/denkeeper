//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/llm"
)

func TestClearSession_RemovesMessages(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	// Send a chat message to create a session with messages.
	chatReq := h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message":    "hello",
		"session_id": "clear-test",
	})
	chatRec := h.Do(chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("chat: status = %d, want %d; body: %s", chatRec.Code, http.StatusOK, chatRec.Body.String())
	}

	// Verify messages exist.
	msgs, err := h.Memory.GetMessages(ctx, "clear-test", 100)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}

	// Clear the session.
	clearReq := h.AuthedRequest(http.MethodPost, "/api/v1/sessions/clear-test/clear", nil)
	clearRec := h.Do(clearReq)
	if clearRec.Code != http.StatusNoContent {
		t.Fatalf("clear: status = %d, want %d; body: %s", clearRec.Code, http.StatusNoContent, clearRec.Body.String())
	}

	// Messages should be gone.
	msgs, err = h.Memory.GetMessages(ctx, "clear-test", 100)
	if err != nil {
		t.Fatalf("GetMessages after clear: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(msgs))
	}

	// Conversation row should still exist.
	convos, _ := h.Memory.ListConversations(ctx)
	found := false
	for _, c := range convos {
		if c.ID == "clear-test" {
			found = true
			break
		}
	}
	if !found {
		t.Error("conversation row should still exist after clear")
	}
}

func TestClearSession_Idempotent(t *testing.T) {
	h := NewHarness(t, nil)

	// Clear a non-existent session should succeed (no-op).
	req := h.AuthedRequest(http.MethodPost, "/api/v1/sessions/nonexistent/clear", nil)
	rec := h.Do(req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestCompactSession_SummarizesMessages(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Responses: []*llm.ChatResponse{
			// First response: for the initial chat message.
			{Content: "Hello there!", TokensUsed: llm.TokenUsage{Total: 15}, Model: "test-model", FinishReason: "stop"},
			// Second response: for the compact summarization call.
			{Content: "Summary: user greeted the bot.", TokensUsed: llm.TokenUsage{Total: 20}, Model: "test-model", FinishReason: "stop"},
		},
	})
	ctx := context.Background()

	// Send a chat to create a session with messages.
	chatReq := h.AuthedRequest(http.MethodPost, "/api/v1/chat", map[string]any{
		"message":    "hello",
		"session_id": "compact-test",
	})
	chatRec := h.Do(chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("chat: status = %d; body: %s", chatRec.Code, chatRec.Body.String())
	}

	// Compact the session (pass agent hint since "compact-test" has no agent prefix).
	compactReq := h.AuthedRequest(http.MethodPost, "/api/v1/sessions/compact-test/compact?agent=default", nil)
	compactRec := h.Do(compactReq)
	if compactRec.Code != http.StatusOK {
		t.Fatalf("compact: status = %d; body: %s", compactRec.Code, compactRec.Body.String())
	}

	var result map[string]string
	DecodeJSON(t, compactRec, &result)
	if result["summary"] != "Summary: user greeted the bot." {
		t.Errorf("summary = %q, want %q", result["summary"], "Summary: user greeted the bot.")
	}

	// Session should now have exactly 1 message (the compacted summary).
	msgs, err := h.Memory.GetMessages(ctx, "compact-test", 100)
	if err != nil {
		t.Fatalf("GetMessages after compact: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after compact, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("role = %q, want assistant", msgs[0].Role)
	}
	if msgs[0].Content != "[Session compacted]\n\nSummary: user greeted the bot." {
		t.Errorf("content = %q", msgs[0].Content)
	}
}

func TestCompactSession_TooFewMessages(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	// Create a session with only 1 message. Use "default:" prefix so the
	// agent can be resolved without a query hint.
	_ = h.Memory.GetOrCreateConversationByID(ctx, "default:api:short", "api", "short")
	_, _ = h.Memory.AddMessage(ctx, "default:api:short", agent.StoredMessage{Role: "user", Content: "hi"})

	req := h.AuthedRequest(http.MethodPost, "/api/v1/sessions/default:api:short/compact", nil)
	rec := h.Do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCompactSession_ExplicitAgentHintNotFound(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	_ = h.Memory.GetOrCreateConversationByID(ctx, "default:api:x", "api", "x")
	_, _ = h.Memory.AddMessage(ctx, "default:api:x", agent.StoredMessage{Role: "user", Content: "a"})
	_, _ = h.Memory.AddMessage(ctx, "default:api:x", agent.StoredMessage{Role: "assistant", Content: "b"})

	// Explicit bogus agent hint should 404 even if the session ID prefix is valid.
	req := h.AuthedRequest(http.MethodPost, "/api/v1/sessions/default:api:x/compact?agent=nonexistent", nil)
	rec := h.Do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestCompactSession_UnresolvableAgent(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	// Session with unknown agent prefix and no hint — should 404 (no fallback).
	_ = h.Memory.GetOrCreateConversationByID(ctx, "ghost:api:x", "api", "x")
	_, _ = h.Memory.AddMessage(ctx, "ghost:api:x", agent.StoredMessage{Role: "user", Content: "a"})
	_, _ = h.Memory.AddMessage(ctx, "ghost:api:x", agent.StoredMessage{Role: "assistant", Content: "b"})

	req := h.AuthedRequest(http.MethodPost, "/api/v1/sessions/ghost:api:x/compact", nil)
	rec := h.Do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
