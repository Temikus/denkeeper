//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

func singleToolCall(name, args string) []*llm.ChatResponse {
	return []*llm.ChatResponse{
		{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: name, Arguments: args}}},
			TokensUsed:   llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:        "test-model",
		},
		{Content: "done", FinishReason: "stop", TokensUsed: llm.TokenUsage{Total: 5}, Model: "test-model"},
	}
}

func chatOnce(t *testing.T, h *Harness, message string) {
	t.Helper()
	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{"message": message}))
	if rec.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", rec.Code, rec.Body.String())
	}
}

func toolMessage(t *testing.T, req llm.ChatRequest) string {
	t.Helper()
	for _, m := range req.Messages {
		if m.Role == "tool" {
			return m.Content
		}
	}
	t.Fatalf("request has no tool message: %+v", req.Messages)
	return ""
}

func TestUntrustedOutput_ExternalEchoIsWrapped(t *testing.T) {
	h := chatToolHarness(t, singleToolCall("echo", `{"input":"hello"}`))
	chatOnce(t, h, "please call echo")

	msg := toolMessage(t, h.MockLLM.LastRequest())
	if !strings.HasPrefix(msg, "<untrusted_tool_output source=\"echo-tool\" id=\"") ||
		!strings.Contains(msg, "\n</untrusted_tool_output id=\"") {
		t.Errorf("echo tool message = %q, want it wrapped with source echo-tool", msg)
	}
	sys := h.MockLLM.LastRequest().Messages[0].Content
	if !strings.Contains(sys, "## Untrusted Tool Output") {
		t.Error("system prompt lacks the untrusted-output note")
	}
}

func TestUntrustedOutput_ConfigToolIsNotWrapped(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := startTestMCPServer(t)
	toolMgr := tool.NewManager(logger)
	t.Cleanup(func() { _ = toolMgr.Close() })
	if err := toolMgr.RegisterServer(context.Background(), "echo-tool", config.ToolConfig{
		Transport: "sse", URL: ts.URL, AllowLoopback: true,
	}); err != nil {
		t.Fatalf("registering echo server: %v", err)
	}
	store, err := kv.NewInMemoryStore()
	if err != nil {
		t.Fatalf("kv store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session, err := configmcp.New(configmcp.Deps{
		AgentName:      "default",
		PermissionTier: func() string { return "autonomous" },
		KVStore:        store,
		Logger:         logger,
	}).Connect(context.Background())
	if err != nil {
		t.Fatalf("config MCP connect: %v", err)
	}
	if err := toolMgr.RegisterSession(context.Background(), "config-default", session); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	h := NewHarness(t, &HarnessOpts{
		Responses:   singleToolCall("kv_get", `{"key":"missing"}`),
		Agents:      []agentSetup{{Name: "default", Tier: "autonomous", Adapters: []string{"api"}}},
		ToolManager: toolMgr,
	})

	chatOnce(t, h, "read the key")
	msg := toolMessage(t, h.MockLLM.LastRequest())
	if strings.HasPrefix(msg, "Tool error") {
		t.Fatalf("kv_get did not run: %q", msg)
	}
	if strings.Contains(msg, "untrusted_tool_output") {
		t.Errorf("config tool message was wrapped: %q", msg)
	}
}
