package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func TestNewProvider_DuckDuckGo(t *testing.T) {
	p, err := NewProvider(config.WebSearchConfig{Provider: "duckduckgo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "duckduckgo" {
		t.Errorf("name = %q, want %q", p.Name(), "duckduckgo")
	}
}

func TestNewProvider_Tavily(t *testing.T) {
	p, err := NewProvider(config.WebSearchConfig{Provider: "tavily", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "tavily" {
		t.Errorf("name = %q, want %q", p.Name(), "tavily")
	}
}

func TestNewProvider_TavilyMissingKey(t *testing.T) {
	_, err := NewProvider(config.WebSearchConfig{Provider: "tavily"})
	if err == nil {
		t.Fatal("expected error for tavily without api_key")
	}
}

func TestNewProvider_Unknown(t *testing.T) {
	_, err := NewProvider(config.WebSearchConfig{Provider: "google"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestTavily_Search(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth = %q, want Bearer test-key", r.Header.Get("Authorization"))
		}

		var req tavilyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if req.Query != "golang testing" {
			t.Errorf("query = %q, want %q", req.Query, "golang testing")
		}

		resp := tavilyResponse{
			Results: []tavilyResult{
				{Title: "Go Testing", URL: "https://go.dev/doc/test", Content: "Testing in Go"},
				{Title: "Go Blog", URL: "https://go.dev/blog", Content: "Go blog posts"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	tv := NewTavily("test-key")
	tv.baseURL = srv.URL

	results, err := tv.Search(context.Background(), "golang testing", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Go Testing" {
		t.Errorf("result[0].title = %q, want %q", results[0].Title, "Go Testing")
	}
	if results[0].URL != "https://go.dev/doc/test" {
		t.Errorf("result[0].url = %q", results[0].URL)
	}
}

func TestTavily_SearchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, "invalid api key")
	}))
	defer srv.Close()

	tv := NewTavily("bad-key")
	tv.baseURL = srv.URL

	_, err := tv.Search(context.Background(), "test", 5)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

func TestDuckDuckGo_SearchLite(t *testing.T) {
	// Serve a mock DDG lite HTML page with result structure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lite/" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>
				<table>
					<tr>
						<td><a rel="nofollow" href="https://example.com/page1">Example Page 1</a></td>
					</tr>
					<tr>
						<td class="result-snippet">This is the first result snippet.</td>
					</tr>
					<tr>
						<td><a rel="nofollow" href="https://example.com/page2">Example Page 2</a></td>
					</tr>
					<tr>
						<td class="result-snippet">This is the second result snippet.</td>
					</tr>
				</table>
			</body></html>`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ddg := NewDuckDuckGo()
	ddg.baseURL = srv.URL

	results, err := ddg.Search(context.Background(), "test query", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].URL != "https://example.com/page1" {
		t.Errorf("result[0].url = %q, want https://example.com/page1", results[0].URL)
	}
}

func TestDuckDuckGo_SearchInstantAnswer(t *testing.T) {
	// Mock the lite endpoint to return no results, and instant answer to return data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lite/" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><table></table></body></html>`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// The instant answer API goes to api.duckduckgo.com, not our mock.
	// So this test just verifies the lite HTML parsing path works with no results.
	ddg := NewDuckDuckGo()
	ddg.baseURL = srv.URL

	results, err := ddg.Search(context.Background(), "test", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Instant answer API call may fail (goes to real DDG), but shouldn't error.
	_ = results
}

func TestDuckDuckGo_MaxResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body><table>
			<tr><td><a rel="nofollow" href="https://example.com/1">R1</a></td></tr>
			<tr><td class="result-snippet">S1</td></tr>
			<tr><td><a rel="nofollow" href="https://example.com/2">R2</a></td></tr>
			<tr><td class="result-snippet">S2</td></tr>
			<tr><td><a rel="nofollow" href="https://example.com/3">R3</a></td></tr>
			<tr><td class="result-snippet">S3</td></tr>
		</table></body></html>`)
	}))
	defer srv.Close()

	ddg := NewDuckDuckGo()
	ddg.baseURL = srv.URL

	results, err := ddg.Search(context.Background(), "test", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) > 2 {
		t.Errorf("got %d results, want <= 2", len(results))
	}
}

func TestParseLiteHTML_Empty(t *testing.T) {
	results := parseLiteHTML("<html><body></body></html>", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty HTML, got %d", len(results))
	}
}

func TestDuckDuckGo_LiteHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ddg := NewDuckDuckGo()
	ddg.baseURL = srv.URL

	_, err := ddg.Search(context.Background(), "test", 5)
	if err == nil {
		t.Fatal("expected error for 503 from DDG lite")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention 503: %v", err)
	}
}

func TestTavily_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results": []}`)
	}))
	defer srv.Close()

	tv := NewTavily("test-key")
	tv.baseURL = srv.URL

	results, err := tv.Search(context.Background(), "nonexistent", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestTruncateText(t *testing.T) {
	short := "hello"
	if truncateText(short, 10) != "hello" {
		t.Errorf("short text should not be truncated")
	}
	long := "hello world this is a long text"
	result := truncateText(long, 10)
	if result != "hello worl..." {
		t.Errorf("truncated = %q, want %q", result, "hello worl...")
	}
}
