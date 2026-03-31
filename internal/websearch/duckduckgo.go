package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// DuckDuckGo implements web search using DuckDuckGo's lite HTML interface.
// This is a free, no-API-key-required provider. It scrapes the lightweight
// HTML search page (lite.duckduckgo.com) for results.
type DuckDuckGo struct {
	client  *http.Client
	baseURL string // overridable for testing
}

// NewDuckDuckGo creates a DuckDuckGo search provider.
func NewDuckDuckGo() *DuckDuckGo {
	return &DuckDuckGo{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: "https://lite.duckduckgo.com",
	}
}

// Name returns the provider identifier.
func (d *DuckDuckGo) Name() string { return "duckduckgo" }

// Search performs a DuckDuckGo search using the lite HTML endpoint.
// It also tries the instant answer API for quick results.
func (d *DuckDuckGo) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	// Try the lite HTML search (more reliable for web results).
	results, err := d.searchLite(ctx, query, maxResults)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo search: %w", err)
	}

	// If lite search returned nothing, try instant answer API as fallback.
	if len(results) == 0 {
		iaResults, iaErr := d.searchInstantAnswer(ctx, query, maxResults)
		if iaErr == nil && len(iaResults) > 0 {
			return iaResults, nil
		}
	}

	return results, nil
}

// searchLite scrapes DDG's lite HTML search page for results.
func (d *DuckDuckGo) searchLite(ctx context.Context, query string, maxResults int) ([]Result, error) {
	form := url.Values{}
	form.Set("q", query)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+"/lite/", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Denkeeper/1.0 (+https://denkeeper.io)")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024)) // 1MB
	if err != nil {
		return nil, err
	}

	return parseLiteHTML(string(body), maxResults), nil
}

// parseLiteHTML extracts search results from DDG lite HTML.
// The lite page uses a simple table structure with result links and snippets.
func parseLiteHTML(htmlContent string, maxResults int) []Result {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	var results []Result
	var currentResult *Result

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}

		if n.Type == html.ElementNode {
			switch {
			case n.Data == "a":
				currentResult = tryParseResultLink(n, currentResult)
			case n.Data == "td" && hasClass(n, "result-snippet") && currentResult != nil:
				currentResult.Snippet = strings.TrimSpace(extractText(n))
				results = append(results, *currentResult)
				currentResult = nil
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}

		// If we found a link but no snippet yet, and we're leaving a table row,
		// commit the result with empty snippet.
		if n.Type == html.ElementNode && n.Data == "tr" && currentResult != nil {
			results = append(results, *currentResult)
			currentResult = nil
		}
	}
	walk(doc)

	// Commit last result if pending.
	if currentResult != nil && len(results) < maxResults {
		results = append(results, *currentResult)
	}

	return results
}

// tryParseResultLink checks if an <a> node is a DDG result link and returns
// a new Result if so. It checks both class="result-link" and rel="nofollow" patterns.
func tryParseResultLink(n *html.Node, current *Result) *Result {
	href := getAttr(n, "href")
	text := extractText(n)
	if href == "" || text == "" {
		return current
	}

	// Pattern 1: <a class="result-link">
	if hasClass(n, "result-link") && !strings.HasPrefix(href, "/lite") {
		return &Result{Title: strings.TrimSpace(text), URL: href}
	}

	// Pattern 2: <a rel="nofollow"> (fallback for simpler DDG lite format)
	if current == nil && getAttr(n, "rel") == "nofollow" &&
		strings.HasPrefix(href, "http") && !strings.Contains(href, "duckduckgo.com") {
		return &Result{Title: strings.TrimSpace(text), URL: href}
	}

	return current
}

// hasClass returns true if the node has an HTML class attribute containing cls.
func hasClass(n *html.Node, cls string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" && strings.Contains(attr.Val, cls) {
			return true
		}
	}
	return false
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var buf strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		buf.WriteString(extractText(c))
	}
	return buf.String()
}

// searchInstantAnswer uses DDG's public instant answer API.
// This returns limited results but works without scraping.
func (d *DuckDuckGo) searchInstantAnswer(ctx context.Context, query string, maxResults int) ([]Result, error) {
	u := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Denkeeper/1.0 (+https://denkeeper.io)")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var ddgResp struct {
		Abstract       string `json:"Abstract"`
		AbstractURL    string `json:"AbstractURL"`
		AbstractSource string `json:"AbstractSource"`
		RelatedTopics  []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ddgResp); err != nil {
		return nil, err
	}

	var results []Result

	if ddgResp.Abstract != "" && ddgResp.AbstractURL != "" {
		results = append(results, Result{
			Title:   ddgResp.AbstractSource,
			URL:     ddgResp.AbstractURL,
			Snippet: ddgResp.Abstract,
		})
	}

	for _, rt := range ddgResp.RelatedTopics {
		if len(results) >= maxResults {
			break
		}
		if rt.FirstURL != "" && rt.Text != "" {
			results = append(results, Result{
				Title:   truncateText(rt.Text, 80),
				URL:     rt.FirstURL,
				Snippet: rt.Text,
			})
		}
	}

	return results, nil
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
