//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// echoParams is the input for the echo MCP tool.
type echoParams struct {
	Input string `json:"input"`
}

// echoHandler returns the input text as-is.
func echoHandler(_ context.Context, _ *mcp.CallToolRequest, args echoParams) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("echo: %s", args.Input)},
		},
	}, nil, nil
}

// startTestMCPServer creates a minimal MCP server with an "echo" tool served
// over Streamable HTTP. Returns an httptest.Server that is automatically
// cleaned up when the test completes.
func startTestMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-echo", Version: "v1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Returns the input text",
	}, echoHandler)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server }, nil,
	)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}
