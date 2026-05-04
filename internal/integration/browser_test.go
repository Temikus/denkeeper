//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/tool"
)

// ---------------------------------------------------------------------------
// Browser tool E2E tests — verifies the full path from user message through
// the Engine tool-call loop to browser MCP tools and back.
// ---------------------------------------------------------------------------

type navigateParams struct {
	URL string `json:"url"`
}

type screenshotParams struct {
	Selector string `json:"selector"`
}

type clickParams struct {
	Selector string `json:"selector"`
}

func navigateHandler(_ context.Context, _ *mcp.CallToolRequest, args navigateParams) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Navigated to %s. Title: Test Page. Content: Welcome to the test page.", args.URL)},
		},
	}, nil, nil
}

func screenshotHandler(_ context.Context, _ *mcp.CallToolRequest, args screenshotParams) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Screenshot captured for selector %s. [base64-image-placeholder]", args.Selector)},
		},
	}, nil, nil
}

func clickHandler(_ context.Context, _ *mcp.CallToolRequest, args clickParams) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Clicked element matching selector %s. Action completed.", args.Selector)},
		},
	}, nil, nil
}

func startBrowserMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-browser", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_navigate",
		Description: "Navigate the browser to a URL",
	}, navigateHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_screenshot",
		Description: "Take a screenshot of an element",
	}, screenshotHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_click",
		Description: "Click an element on the page",
	}, clickHandler)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server }, nil,
	)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func browserToolHarness(t *testing.T, responses []*llm.ChatResponse) *Harness {
	t.Helper()

	ts := startBrowserMCPServer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolMgr := tool.NewManager(logger)

	err := toolMgr.RegisterServer(context.Background(), "browser", config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("registering browser MCP server: %v", err)
	}
	t.Cleanup(func() { _ = toolMgr.Close() })

	return NewHarness(t, &HarnessOpts{
		Responses: responses,
		Agents: []agentSetup{
			{Name: "default", Tier: "autonomous", Adapters: []string{"api"}},
		},
		ToolManager: toolMgr,
	})
}

func TestBrowser_NavigateHappyPath(t *testing.T) {
	h := browserToolHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "browser_navigate",
						Arguments: `{"url":"https://example.com"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "The page title is Test Page and it says Welcome to the test page.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	req := h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "navigate to example.com and tell me what you see",
	})
	req.Header.Set("Accept", "text/event-stream")
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	if !strings.Contains(body, `"type":"tool_start"`) {
		t.Error("SSE stream missing tool_start event")
	}
	if !strings.Contains(body, `"type":"tool_end"`) {
		t.Error("SSE stream missing tool_end event")
	}
	if !strings.Contains(body, `"type":"content"`) {
		t.Error("SSE stream missing content event")
	}
	if !strings.Contains(body, "The page title is Test Page") {
		t.Error("SSE stream missing final response text")
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestBrowser_MultiToolSequence(t *testing.T) {
	h := browserToolHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "browser_navigate",
						Arguments: `{"url":"https://example.com/login"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_2",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "browser_click",
						Arguments: `{"selector":"#submit-btn"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 30, Completion: 5, Total: 35},
			Model:      "test-model",
		},
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_3",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "browser_screenshot",
						Arguments: `{"selector":"body"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 50, Completion: 5, Total: 55},
			Model:      "test-model",
		},
		{
			Content:      "Login page loaded, submit button clicked, and screenshot captured successfully.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 70, Completion: 15, Total: 85},
			Model:        "test-model",
		},
	})

	req := h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "go to the login page, click submit, and take a screenshot",
	})
	req.Header.Set("Accept", "text/event-stream")
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	toolStartCount := strings.Count(body, `"type":"tool_start"`)
	toolEndCount := strings.Count(body, `"type":"tool_end"`)
	if toolStartCount != 3 {
		t.Errorf("expected 3 tool_start events, got %d", toolStartCount)
	}
	if toolEndCount != 3 {
		t.Errorf("expected 3 tool_end events, got %d", toolEndCount)
	}
	if !strings.Contains(body, "Login page loaded") {
		t.Error("SSE stream missing final response text")
	}

	if h.MockLLM.CallCount() != 4 {
		t.Errorf("expected 4 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestBrowser_NavigateJSON(t *testing.T) {
	h := browserToolHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "browser_navigate",
						Arguments: `{"url":"https://example.com"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "The page says Welcome to the test page.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "navigate to example.com",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	DecodeJSON(t, rec, &resp)
	if !strings.Contains(resp["response"], "Welcome to the test page") {
		t.Errorf("response = %q, want to contain 'Welcome to the test page'", resp["response"])
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}

func TestBrowser_ToolResultFedBackToLLM(t *testing.T) {
	h := browserToolHarness(t, []*llm.ChatResponse{
		{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call_nav",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "browser_navigate",
						Arguments: `{"url":"https://docs.example.com/api"}`,
					},
				},
			},
			TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
			Model:      "test-model",
		},
		{
			Content:      "Found the API docs.",
			FinishReason: "stop",
			TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
			Model:        "test-model",
		},
	})

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "open the API docs",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	lastReq := h.MockLLM.LastRequest()
	var foundToolResult bool
	for _, msg := range lastReq.Messages {
		if msg.Role == "tool" && strings.Contains(msg.Content, "Navigated to https://docs.example.com/api") {
			foundToolResult = true
			break
		}
	}
	if !foundToolResult {
		t.Error("expected tool result message containing navigate output in second LLM call")
	}
}

func TestBrowser_SupervisedApproval(t *testing.T) {
	ts := startBrowserMCPServer(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	toolMgr := tool.NewManager(logger)
	err := toolMgr.RegisterServer(context.Background(), "browser", config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("registering browser MCP server: %v", err)
	}
	t.Cleanup(func() { _ = toolMgr.Close() })

	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "default", Tier: "supervised"},
		},
		ToolManager: toolMgr,
		Responses: []*llm.ChatResponse{
			{
				Content:      "",
				FinishReason: "tool_calls",
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "browser_navigate",
							Arguments: `{"url":"https://example.com"}`,
						},
					},
				},
				TokensUsed: llm.TokenUsage{Prompt: 10, Completion: 5, Total: 15},
				Model:      "test-model",
			},
			{
				Content:      "Navigated successfully after approval.",
				FinishReason: "stop",
				TokensUsed:   llm.TokenUsage{Prompt: 20, Completion: 10, Total: 30},
				Model:        "test-model",
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go approvalWorker(ctx, h, true)

	rec := h.Do(h.AuthedRequest("POST", "/api/v1/chat", map[string]string{
		"message": "navigate to example.com",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !strings.Contains(resp["response"], "Navigated successfully after approval") {
		t.Errorf("response = %q, want to contain approval confirmation", resp["response"])
	}

	if h.MockLLM.CallCount() != 2 {
		t.Errorf("expected 2 LLM calls, got %d", h.MockLLM.CallCount())
	}
}
