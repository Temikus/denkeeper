package webmcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxResponseChars = 8000

func (s *Server) registerTools() {
	if s.deps.SearchProvider != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "web_search",
			Description: "Search the web for information. Returns titles, URLs, and snippets.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"max_results": {"type": "integer", "description": "Maximum number of results to return (default 5, max 10)"}
				},
				"required": ["query"]
			}`),
		}, s.handleWebSearch)
	}

	if s.deps.Fetcher != nil {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        "web_fetch",
			Description: "Fetch a URL and convert its content to Markdown. Use for reading web pages, documentation, articles. Returns truncated content with pagination support via start_index.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL to fetch (must be http or https)"},
					"start_index": {"type": "integer", "description": "Character offset for reading large pages (default 0). Use when previous fetch indicated more content is available."}
				},
				"required": ["url"]
			}`),
		}, s.handleWebFetch)
	}
}

func (s *Server) handleWebSearch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.PermissionTier() == "restricted" {
		return toolError("web_search is not available in restricted mode"), nil
	}

	var input struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if input.Query == "" {
		return toolError("query is required"), nil
	}
	if input.MaxResults <= 0 {
		input.MaxResults = 5
	}
	if input.MaxResults > 10 {
		input.MaxResults = 10
	}

	results, err := s.deps.SearchProvider.Search(ctx, input.Query, input.MaxResults)
	if err != nil {
		s.deps.Logger.Warn("web search failed", "query", input.Query, "provider", s.deps.SearchProvider.Name(), "error", err)
		return toolError(fmt.Sprintf("search failed: %v", err)), nil
	}

	type resultJSON struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	}

	out := make([]resultJSON, len(results))
	for i, r := range results {
		out[i] = resultJSON{Title: r.Title, URL: r.URL, Snippet: r.Snippet}
	}

	resp, _ := json.Marshal(map[string]any{
		"provider":     s.deps.SearchProvider.Name(),
		"result_count": len(out),
		"results":      out,
	})

	return toolText(string(resp)), nil
}

func (s *Server) handleWebFetch(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.PermissionTier() == "restricted" {
		return toolError("web_fetch is not available in restricted mode"), nil
	}

	var input struct {
		URL        string `json:"url"`
		StartIndex int    `json:"start_index"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if input.URL == "" {
		return toolError("url is required"), nil
	}

	result, err := s.deps.Fetcher.Fetch(ctx, input.URL)
	if err != nil {
		s.deps.Logger.Warn("web fetch failed", "url", input.URL, "error", err)
		return toolError(fmt.Sprintf("fetch failed: %v", err)), nil
	}

	content := result.Content
	totalLength := len(content)

	// Apply pagination.
	if input.StartIndex > 0 {
		if input.StartIndex >= len(content) {
			return toolText(`{"content": "", "has_more": false}`), nil
		}
		content = content[input.StartIndex:]
	}

	hasMore := false
	if len(content) > maxResponseChars {
		content = content[:maxResponseChars]
		hasMore = true
	}

	resp, _ := json.Marshal(map[string]any{
		"url":          result.URL,
		"title":        result.Title,
		"content":      content,
		"content_type": result.ContentType,
		"total_length": totalLength,
		"start_index":  input.StartIndex,
		"has_more":     hasMore,
	})

	return toolText(string(resp)), nil
}

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}
