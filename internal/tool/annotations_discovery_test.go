package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/config"
)

type annotToolArgs struct {
	Query string `json:"query"`
}

func annotToolHandler(_ context.Context, _ *mcp.CallToolRequest, _ annotToolArgs) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
	}, nil, nil
}

// startAnnotatedServer serves an MCP server with one readOnlyHint-annotated
// tool, one idempotentHint-only tool, and one unannotated tool.
func startAnnotatedServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "annotated", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ro_lookup",
		Description: "read-only lookup",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, annotToolHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "idem_write",
		Description: "idempotent write",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}, annotToolHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "plain_write",
		Description: "unannotated",
	}, annotToolHandler)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

// TestDiscoverTools_CapturesReadOnlyHint pins the full path: RegisterServer →
// discoverTools → readOnlyHinted → IsIdempotent, and that only readOnlyHint
// qualifies — idempotentHint permits state-changing first calls, which
// memoization must never skip.
func TestDiscoverTools_CapturesReadOnlyHint(t *testing.T) {
	ts := startAnnotatedServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	if err := m.RegisterServer(context.Background(), "annotated", config.ToolConfig{
		Transport:        "sse",
		URL:              ts.URL,
		AllowLoopback:    true,
		TrustAnnotations: true,
	}); err != nil {
		t.Fatalf("RegisterServer: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if !m.IsIdempotent("ro_lookup") {
		t.Error("IsIdempotent(ro_lookup) = false, want true (readOnlyHint captured at discovery)")
	}
	if m.IsIdempotent("idem_write") {
		t.Error("IsIdempotent(idem_write) = true, want false (idempotentHint alone must not qualify)")
	}
	if m.IsIdempotent("plain_write") {
		t.Error("IsIdempotent(plain_write) = true, want false (unannotated)")
	}
}

// TestDiscoverTools_HintInertWithoutTrust registers the same annotated server
// without trust_annotations: the hint is captured but must change nothing.
func TestDiscoverTools_HintInertWithoutTrust(t *testing.T) {
	ts := startAnnotatedServer(t)
	m := NewManager(testLogger(), config.MCPConfig{RequestTimeoutSecs: 10})
	if err := m.RegisterServer(context.Background(), "annotated", config.ToolConfig{
		Transport:     "sse",
		URL:           ts.URL,
		AllowLoopback: true,
	}); err != nil {
		t.Fatalf("RegisterServer: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if m.IsIdempotent("ro_lookup") {
		t.Error("IsIdempotent(ro_lookup) = true, want false (annotations untrusted by default)")
	}
}
