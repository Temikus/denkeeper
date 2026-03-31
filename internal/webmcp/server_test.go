package webmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/webfetch"
	"github.com/Temikus/denkeeper/internal/websearch"
)

// mockSearchProvider is a test double for websearch.Provider.
type mockSearchProvider struct {
	results []websearch.Result
	err     error
}

func (m *mockSearchProvider) Search(_ context.Context, _ string, _ int) ([]websearch.Result, error) {
	return m.results, m.err
}
func (m *mockSearchProvider) Name() string { return "mock" }

// mockFetcher is a test double for webfetch.Fetcher.
type mockFetcher struct {
	result *webfetch.FetchResult
	err    error
}

func (m *mockFetcher) Fetch(_ context.Context, _ string) (*webfetch.FetchResult, error) {
	return m.result, m.err
}
func (m *mockFetcher) Name() string { return "mock" }

func newTestServer(t *testing.T, deps Deps) *mcp.ClientSession {
	t.Helper()
	srv := New(deps)
	ctx := context.Background()
	session, err := srv.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("call tool %q: %v", name, err)
	}
	return result
}

func extractText(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func TestWebSearch_Success(t *testing.T) {
	session := newTestServer(t, Deps{
		SearchProvider: &mockSearchProvider{
			results: []websearch.Result{
				{Title: "Go", URL: "https://go.dev", Snippet: "The Go language"},
				{Title: "Rust", URL: "https://rust-lang.org", Snippet: "The Rust language"},
			},
		},
		PermissionTier: func() string { return "autonomous" },
	})

	result := callTool(t, session, "web_search", map[string]any{"query": "programming"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}

	text := extractText(result)
	var resp struct {
		ResultCount int `json:"result_count"`
		Results     []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ResultCount != 2 {
		t.Errorf("result_count = %d, want 2", resp.ResultCount)
	}
	if resp.Results[0].Title != "Go" {
		t.Errorf("results[0].title = %q, want %q", resp.Results[0].Title, "Go")
	}
}

func TestWebSearch_Restricted(t *testing.T) {
	session := newTestServer(t, Deps{
		SearchProvider: &mockSearchProvider{},
		PermissionTier: func() string { return "restricted" },
	})

	result := callTool(t, session, "web_search", map[string]any{"query": "test"})
	if !result.IsError {
		t.Fatal("expected error for restricted tier")
	}
	if !strings.Contains(extractText(result), "restricted") {
		t.Errorf("error should mention restricted: %s", extractText(result))
	}
}

func TestWebSearch_EmptyQuery(t *testing.T) {
	session := newTestServer(t, Deps{
		SearchProvider: &mockSearchProvider{},
		PermissionTier: func() string { return "autonomous" },
	})

	result := callTool(t, session, "web_search", map[string]any{"query": ""})
	if !result.IsError {
		t.Fatal("expected error for empty query")
	}
}

func TestWebFetch_Success(t *testing.T) {
	session := newTestServer(t, Deps{
		Fetcher: &mockFetcher{
			result: &webfetch.FetchResult{
				URL:         "https://example.com",
				Title:       "Example",
				Content:     "# Example\n\nThis is example content.",
				ContentType: "text/html",
			},
		},
		PermissionTier: func() string { return "autonomous" },
	})

	result := callTool(t, session, "web_fetch", map[string]any{"url": "https://example.com"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}

	text := extractText(result)
	var resp struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Content string `json:"content"`
		HasMore bool   `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Title != "Example" {
		t.Errorf("title = %q, want %q", resp.Title, "Example")
	}
	if !strings.Contains(resp.Content, "Example") {
		t.Errorf("content should contain Example: %s", resp.Content)
	}
	if resp.HasMore {
		t.Error("has_more should be false for short content")
	}
}

func TestWebFetch_Pagination(t *testing.T) {
	longContent := strings.Repeat("x", 10000)
	session := newTestServer(t, Deps{
		Fetcher: &mockFetcher{
			result: &webfetch.FetchResult{
				URL:     "https://example.com",
				Content: longContent,
			},
		},
		PermissionTier: func() string { return "autonomous" },
	})

	// First page.
	result := callTool(t, session, "web_fetch", map[string]any{"url": "https://example.com"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}

	var resp struct {
		Content     string `json:"content"`
		TotalLength int    `json:"total_length"`
		HasMore     bool   `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(extractText(result)), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.HasMore {
		t.Error("has_more should be true for long content")
	}
	if resp.TotalLength != 10000 {
		t.Errorf("total_length = %d, want 10000", resp.TotalLength)
	}
	if len(resp.Content) != maxResponseChars {
		t.Errorf("content length = %d, want %d", len(resp.Content), maxResponseChars)
	}

	// Second page.
	result2 := callTool(t, session, "web_fetch", map[string]any{
		"url":         "https://example.com",
		"start_index": maxResponseChars,
	})
	var resp2 struct {
		Content string `json:"content"`
		HasMore bool   `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(extractText(result2)), &resp2); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp2.Content) != 10000-maxResponseChars {
		t.Errorf("second page content length = %d, want %d", len(resp2.Content), 10000-maxResponseChars)
	}
	if resp2.HasMore {
		t.Error("second page should not have more")
	}
}

func TestWebFetch_Restricted(t *testing.T) {
	session := newTestServer(t, Deps{
		Fetcher:        &mockFetcher{},
		PermissionTier: func() string { return "restricted" },
	})

	result := callTool(t, session, "web_fetch", map[string]any{"url": "https://example.com"})
	if !result.IsError {
		t.Fatal("expected error for restricted tier")
	}
}

func TestWebFetch_EmptyURL(t *testing.T) {
	session := newTestServer(t, Deps{
		Fetcher:        &mockFetcher{},
		PermissionTier: func() string { return "autonomous" },
	})

	result := callTool(t, session, "web_fetch", map[string]any{"url": ""})
	if !result.IsError {
		t.Fatal("expected error for empty url")
	}
}

func TestToolRegistration_OnlySearch(t *testing.T) {
	session := newTestServer(t, Deps{
		SearchProvider: &mockSearchProvider{},
		PermissionTier: func() string { return "autonomous" },
	})

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools.Tools))
	}
	if tools.Tools[0].Name != "web_search" {
		t.Errorf("tool name = %q, want %q", tools.Tools[0].Name, "web_search")
	}
}

func TestToolRegistration_OnlyFetch(t *testing.T) {
	session := newTestServer(t, Deps{
		Fetcher:        &mockFetcher{},
		PermissionTier: func() string { return "autonomous" },
	})

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools.Tools))
	}
	if tools.Tools[0].Name != "web_fetch" {
		t.Errorf("tool name = %q, want %q", tools.Tools[0].Name, "web_fetch")
	}
}

func TestWebSearch_ProviderError(t *testing.T) {
	session := newTestServer(t, Deps{
		SearchProvider: &mockSearchProvider{
			err: fmt.Errorf("upstream API failure"),
		},
		PermissionTier: func() string { return "autonomous" },
	})

	result := callTool(t, session, "web_search", map[string]any{"query": "test"})
	if !result.IsError {
		t.Fatal("expected error when search provider fails")
	}
	if !strings.Contains(extractText(result), "search failed") {
		t.Errorf("error should mention search failed: %s", extractText(result))
	}
}

func TestWebFetch_FetcherError(t *testing.T) {
	session := newTestServer(t, Deps{
		Fetcher: &mockFetcher{
			err: fmt.Errorf("connection refused"),
		},
		PermissionTier: func() string { return "autonomous" },
	})

	result := callTool(t, session, "web_fetch", map[string]any{"url": "https://example.com"})
	if !result.IsError {
		t.Fatal("expected error when fetcher fails")
	}
	if !strings.Contains(extractText(result), "fetch failed") {
		t.Errorf("error should mention fetch failed: %s", extractText(result))
	}
}

func TestWebFetch_StartIndexBeyondContent(t *testing.T) {
	session := newTestServer(t, Deps{
		Fetcher: &mockFetcher{
			result: &webfetch.FetchResult{
				URL:     "https://example.com",
				Content: "short",
			},
		},
		PermissionTier: func() string { return "autonomous" },
	})

	result := callTool(t, session, "web_fetch", map[string]any{
		"url":         "https://example.com",
		"start_index": 9999,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}

	var resp struct {
		Content string `json:"content"`
		HasMore bool   `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(extractText(result)), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("content should be empty for start_index beyond content, got: %q", resp.Content)
	}
	if resp.HasMore {
		t.Error("has_more should be false")
	}
}

func TestWebSearch_MaxResultsCapped(t *testing.T) {
	session := newTestServer(t, Deps{
		SearchProvider: &mockSearchProvider{
			results: []websearch.Result{
				{Title: "R1", URL: "https://example.com/1", Snippet: "s1"},
			},
		},
		PermissionTier: func() string { return "autonomous" },
	})

	// max_results > 10 should be capped to 10 (no error).
	result := callTool(t, session, "web_search", map[string]any{"query": "test", "max_results": 50})
	if result.IsError {
		t.Fatalf("unexpected error: %s", extractText(result))
	}
}

func TestToolRegistration_NoneRegistered(t *testing.T) {
	session := newTestServer(t, Deps{
		PermissionTier: func() string { return "autonomous" },
	})

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 0 {
		t.Fatalf("expected 0 tools when no provider/fetcher, got %d", len(tools.Tools))
	}
}

func TestToolRegistration_Both(t *testing.T) {
	session := newTestServer(t, Deps{
		SearchProvider: &mockSearchProvider{},
		Fetcher:        &mockFetcher{},
		PermissionTier: func() string { return "autonomous" },
	})

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools.Tools))
	}
}
