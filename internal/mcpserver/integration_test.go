//go:build integration

package mcpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testDeps(t *testing.T) mcpserver.Deps {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger)
	return mcpserver.Deps{
		Dispatcher: dispatcher,
		TOMLKeys: []config.APIKeyConfig{
			{Name: "test-key", Key: "dk-test-integration-key", Scopes: []string{"admin"}},
		},
		Logger: logger,
	}
}

func startMCPServer(t *testing.T, deps mcpserver.Deps) *httptest.Server {
	t.Helper()
	cfg := config.APIMCPServerConfig{
		Transport:      "streamable",
		SessionTimeout: "30m",
		ChatTimeout:    "2m",
	}
	srv := mcpserver.New(cfg, deps)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func connectClient(t *testing.T, ts *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL,
		HTTPClient: &http.Client{
			Transport: &bearerTransport{token: token, base: http.DefaultTransport},
		},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (bt *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+bt.token)
	return bt.base.RoundTrip(req)
}

func TestMCPServer_Auth_NoToken(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)

	transport := &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	_, err := client.Connect(context.Background(), transport, nil)
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
}

func TestMCPServer_Auth_InvalidToken(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)

	transport := &mcp.StreamableClientTransport{
		Endpoint: ts.URL,
		HTTPClient: &http.Client{
			Transport: &bearerTransport{token: "dk-invalid-key", base: http.DefaultTransport},
		},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	_, err := client.Connect(context.Background(), transport, nil)
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
}

func TestMCPServer_Auth_ValidToken(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)

	session := connectClient(t, ts, "dk-test-integration-key")
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}
}

func TestMCPServer_ToolList(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	toolNames := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true
	}

	expected := []string{"panic", "resume", "panic_status", "agent_list", "agent_info"}
	for _, name := range expected {
		if !toolNames[name] {
			t.Errorf("expected tool %q in list", name)
		}
	}
}

func TestMCPServer_PanicStatus(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "panic_status",
	})
	if err != nil {
		t.Fatalf("call panic_status: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", result.Content[0])
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(text.Text), &status); err != nil {
		t.Fatalf("unmarshal panic_status: %v", err)
	}
	if status["panicked"] != false {
		t.Errorf("expected panicked=false, got %v", status["panicked"])
	}
}

func TestMCPServer_AgentList(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "agent_list",
	})
	if err != nil {
		t.Fatalf("call agent_list: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestMCPServer_Chat_PanicState(t *testing.T) {
	deps := testDeps(t)
	deps.Dispatcher.Panic()
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "chat",
		Arguments: map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("call chat: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when system is panicked")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "panic") {
		t.Errorf("expected panic error message, got: %v", text)
	}
}

func TestMCPServer_Chat_AgentNotFound(t *testing.T) {
	deps := testDeps(t)
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-test-integration-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "chat",
		Arguments: map[string]any{"message": "hello", "agent": "nonexistent-agent"},
	})
	if err != nil {
		t.Fatalf("call chat: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for nonexistent agent")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "not found") {
		t.Errorf("expected 'not found' error message, got: %v", text)
	}
}

func TestMCPServer_Chat_ScopeRequired(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger)
	deps := mcpserver.Deps{
		Dispatcher: dispatcher,
		TOMLKeys: []config.APIKeyConfig{
			{Name: "read-only", Key: "dk-readonly-key", Scopes: []string{"agents:read"}},
		},
		Logger: logger,
	}
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-readonly-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "chat",
		Arguments: map[string]any{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("call chat: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected scope error for chat without chat scope")
	}
	text, _ := result.Content[0].(*mcp.TextContent)
	if text == nil || !strings.Contains(text.Text, "scope") {
		t.Errorf("expected scope error message, got: %v", text)
	}
}

func TestMCPServer_ScopeEnforcement(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{}, nil, nil, logger)
	deps := mcpserver.Deps{
		Dispatcher: dispatcher,
		TOMLKeys: []config.APIKeyConfig{
			{Name: "limited-key", Key: "dk-limited-key", Scopes: []string{"kv:read"}},
		},
		Logger: logger,
	}
	ts := startMCPServer(t, deps)
	session := connectClient(t, ts, "dk-limited-key")

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "panic_status",
	})
	if err != nil {
		t.Fatalf("call panic_status: %v", err)
	}

	if !result.IsError {
		text, _ := result.Content[0].(*mcp.TextContent)
		t.Errorf("expected error result for missing scope, got text: %s", text.Text)
	}
}
