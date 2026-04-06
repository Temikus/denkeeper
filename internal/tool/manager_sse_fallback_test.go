package tool

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
)

// sayHiParams is the input for the sayHi tool.
type sayHiParams struct {
	Name string `json:"name"`
}

// sayHi is a minimal MCP tool handler for testing.
func sayHi(_ context.Context, _ *mcp.CallToolRequest, args sayHiParams) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("hello %s", args.Name)},
		},
	}, nil, nil
}

// newTestMCPServer creates an MCP server with a single "greet" tool.
func newTestMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "greet", Description: "say hello"}, sayHi)
	return server
}

// startStreamableServer starts an httptest server using the Streamable HTTP
// transport (2025-03-26 spec).
func startStreamableServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := newTestMCPServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// startLegacySSEServer starts an httptest server using the legacy SSE
// transport (2024-11-05 spec).
func startLegacySSEServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := newTestMCPServer()
	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestRegisterSSE_StreamableHTTPServer(t *testing.T) {
	ts := startStreamableServer(t)

	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	cfg := config.ToolConfig{
		Transport:      "sse",
		URL:            ts.URL,
		AllowLoopback:  true,
	}

	err := m.RegisterServer(context.Background(), "streamable-tool", cfg)
	if err != nil {
		t.Fatalf("RegisterServer failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// Should have discovered the "greet" tool.
	names := m.ToolNames()
	if len(names) != 1 || names[0] != "greet" {
		t.Errorf("ToolNames() = %v, want [greet]", names)
	}

	// Transport should be "sse" (Streamable HTTP succeeded on first try).
	m.mu.RLock()
	sc := m.servers["streamable-tool"]
	m.mu.RUnlock()
	if sc.transport != "sse" {
		t.Errorf("transport = %q, want %q", sc.transport, "sse")
	}
}

func TestRegisterSSE_LegacySSEFallback(t *testing.T) {
	ts := startLegacySSEServer(t)

	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	cfg := config.ToolConfig{
		Transport:      "sse",
		URL:            ts.URL,
		AllowLoopback:  true,
	}

	err := m.RegisterServer(context.Background(), "legacy-tool", cfg)
	if err != nil {
		t.Fatalf("RegisterServer failed: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// Should have discovered the "greet" tool via legacy SSE.
	names := m.ToolNames()
	if len(names) != 1 || names[0] != "greet" {
		t.Errorf("ToolNames() = %v, want [greet]", names)
	}

	// Transport should indicate the legacy fallback was used.
	m.mu.RLock()
	sc := m.servers["legacy-tool"]
	m.mu.RUnlock()
	if sc.transport != "sse-legacy" {
		t.Errorf("transport = %q, want %q", sc.transport, "sse-legacy")
	}
}

func TestRegisterSSE_BothProtocolsFail(t *testing.T) {
	// A server that always returns 500.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)

	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 5})
	cfg := config.ToolConfig{
		Transport:      "sse",
		URL:            ts.URL,
		AllowLoopback:  true,
	}

	err := m.RegisterServer(context.Background(), "broken-tool", cfg)
	if err == nil {
		t.Fatal("expected error when both protocols fail")
	}

	// Error should mention both transports.
	if !strings.Contains(err.Error(), "Streamable HTTP") || !strings.Contains(err.Error(), "legacy SSE") {
		t.Errorf("error should mention both transports, got: %v", err)
	}
}

func TestRegisterSSE_OAuthNoFallback(t *testing.T) {
	// Start a legacy-only SSE server. OAuth tools should NOT fall back.
	ts := startLegacySSEServer(t)

	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 5})
	// OAuth requires OAuthSupport to be configured.
	// Since we're testing that connectSSE doesn't fall back for OAuth,
	// we set Auth to "oauth" but don't configure OAuthSupport — setupOAuth
	// should fail before we even get to connectSSE.
	cfg := config.ToolConfig{
		Transport:      "sse",
		URL:            ts.URL,
		Auth:           "oauth",
		AllowLoopback:  true,
	}

	err := m.RegisterServer(context.Background(), "oauth-tool", cfg)
	if err == nil {
		t.Fatal("expected error for OAuth tool without OAuthSupport configured")
	}

	// Should mention OAuth configuration, not protocol fallback.
	if !strings.Contains(err.Error(), "OAuth support is not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConnectSSE_OAuthSkipsFallback(t *testing.T) {
	// Test that connectSSE itself does not fall back for OAuth tools,
	// even if the Streamable HTTP connection fails.
	ts := startLegacySSEServer(t)

	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 5})
	httpClient := &http.Client{}
	streamableTransport := &mcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: httpClient,
	}
	cfg := config.ToolConfig{
		Auth: "oauth",
	}

	_, _, err := m.connectSSE(context.Background(), "oauth-test", cfg, httpClient, streamableTransport, ts.URL)
	if err == nil {
		t.Fatal("expected error: OAuth tool should not fall back to legacy SSE")
	}

	// Error should NOT mention legacy SSE fallback.
	if strings.Contains(err.Error(), "legacy SSE") {
		t.Errorf("OAuth tools should not attempt legacy SSE fallback, got: %v", err)
	}
}

func TestConnectSSE_StreamableSucceeds(t *testing.T) {
	ts := startStreamableServer(t)

	m := NewManager(testLogger())
	httpClient := &http.Client{}
	streamableTransport := &mcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: httpClient,
	}
	cfg := config.ToolConfig{}

	session, transport, err := m.connectSSE(context.Background(), "test", cfg, httpClient, streamableTransport, ts.URL)
	if err != nil {
		t.Fatalf("connectSSE failed: %v", err)
	}
	defer func() { _ = session.Close() }()

	if transport != "sse" {
		t.Errorf("transport = %q, want %q", transport, "sse")
	}
}

func TestConnectSSE_FallbackToLegacy(t *testing.T) {
	ts := startLegacySSEServer(t)

	m := NewManager(testLogger())
	httpClient := &http.Client{}
	streamableTransport := &mcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: httpClient,
	}
	cfg := config.ToolConfig{}

	session, transport, err := m.connectSSE(context.Background(), "test", cfg, httpClient, streamableTransport, ts.URL)
	if err != nil {
		t.Fatalf("connectSSE failed: %v", err)
	}
	defer func() { _ = session.Close() }()

	if transport != "sse-legacy" {
		t.Errorf("transport = %q, want %q", transport, "sse-legacy")
	}
}
