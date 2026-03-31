package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Tavily implements web search using the Tavily Search API.
// Tavily is purpose-built for AI agents with prompt injection protection
// and structured results optimized for LLM reasoning.
type Tavily struct {
	client  *http.Client
	apiKey  string
	baseURL string // overridable for testing
}

// NewTavily creates a Tavily search provider.
func NewTavily(apiKey string) *Tavily {
	return &Tavily{
		client:  &http.Client{Timeout: 15 * time.Second},
		apiKey:  apiKey,
		baseURL: "https://api.tavily.com",
	}
}

// Name returns the provider identifier.
func (t *Tavily) Name() string { return "tavily" }

// Search performs a Tavily search.
func (t *Tavily) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	reqBody := tavilyRequest{
		Query:      query,
		MaxResults: maxResults,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("encoding tavily request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating tavily request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("tavily HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var tavilyResp tavilyResponse
	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, fmt.Errorf("decoding tavily response: %w", err)
	}

	results := make([]Result, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		results = append(results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}

	return results, nil
}

type tavilyRequest struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

type tavilyResponse struct {
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}
