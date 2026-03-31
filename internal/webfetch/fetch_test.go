package webfetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaultFetcher_BasicHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><head><title>Test Page</title></head><body><h1>Hello</h1><p>World</p></body></html>`)
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second})
	result, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "Test Page" {
		t.Errorf("title = %q, want %q", result.Title, "Test Page")
	}
	if !strings.Contains(result.Content, "Hello") {
		t.Errorf("content should contain 'Hello', got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "World") {
		t.Errorf("content should contain 'World', got: %s", result.Content)
	}
	if result.URL != srv.URL {
		t.Errorf("url = %q, want %q", result.URL, srv.URL)
	}
}

func TestDefaultFetcher_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "Just plain text content")
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second})
	result, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Just plain text content" {
		t.Errorf("content = %q, want plain text", result.Content)
	}
}

func TestDefaultFetcher_SizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Write 2KB of data.
		_, _ = fmt.Fprint(w, strings.Repeat("x", 2048))
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{
		Timeout:      5 * time.Second,
		MaxSizeBytes: 1024, // 1KB limit
	})
	result, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) > 1024 {
		t.Errorf("content length = %d, should be <= 1024", len(result.Content))
	}
}

func TestDefaultFetcher_HTTP4xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second})
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404: %v", err)
	}
}

func TestDefaultFetcher_InvalidScheme(t *testing.T) {
	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second})
	_, err := f.Fetch(context.Background(), "ftp://example.com/file")
	if err == nil {
		t.Fatal("expected error for ftp scheme")
	}
	if !strings.Contains(err.Error(), "unsupported scheme") {
		t.Errorf("error should mention unsupported scheme: %v", err)
	}
}

func TestDefaultFetcher_Redirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><head><title>Final</title></head><body>Landed</body></html>`)
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second})
	result, err := f.Fetch(context.Background(), srv.URL+"/redirect")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "Final" {
		t.Errorf("title = %q, want %q", result.Title, "Final")
	}
	if !strings.HasSuffix(result.URL, "/final") {
		t.Errorf("final url = %q, should end with /final", result.URL)
	}
}

func TestDefaultFetcher_TooManyRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second})
	_, err := f.Fetch(context.Background(), srv.URL+"/loop")
	if err == nil {
		t.Fatal("expected error for too many redirects")
	}
}

func TestDefaultFetcher_UserAgent(t *testing.T) {
	var receivedUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{
		Timeout:   5 * time.Second,
		UserAgent: "TestBot/1.0",
	})
	_, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedUA != "TestBot/1.0" {
		t.Errorf("user-agent = %q, want %q", receivedUA, "TestBot/1.0")
	}
}

func TestDefaultFetcher_RobotsTxtBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = fmt.Fprint(w, "User-agent: Denkeeper\nDisallow: /blocked\n")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "allowed")
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{
		Timeout:          5 * time.Second,
		RespectRobotsTxt: true,
	})

	// Blocked path should return error.
	_, err := f.Fetch(context.Background(), srv.URL+"/blocked")
	if err == nil {
		t.Fatal("expected error for robots.txt blocked path")
	}
	if !strings.Contains(err.Error(), "blocked by robots.txt") {
		t.Errorf("error should mention robots.txt: %v", err)
	}

	// Allowed path should succeed.
	result, err := f.Fetch(context.Background(), srv.URL+"/allowed")
	if err != nil {
		t.Fatalf("unexpected error for allowed path: %v", err)
	}
	if result.Content != "allowed" {
		t.Errorf("content = %q, want %q", result.Content, "allowed")
	}
}

func TestDefaultFetcher_RobotsTxtNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{
		Timeout:          5 * time.Second,
		RespectRobotsTxt: true,
	})

	// No robots.txt = everything allowed.
	result, err := f.Fetch(context.Background(), srv.URL+"/anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "ok" {
		t.Errorf("content = %q, want %q", result.Content, "ok")
	}
}

func TestDefaultFetcher_AgentsTxtBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agents.txt" {
			_, _ = fmt.Fprint(w, "User-agent: Denkeeper\nDisallow: /private\n")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "public content")
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{
		Timeout:          5 * time.Second,
		RespectAgentsTxt: true,
	})

	_, err := f.Fetch(context.Background(), srv.URL+"/private")
	if err == nil {
		t.Fatal("expected error for agents.txt blocked path")
	}
	if !strings.Contains(err.Error(), "blocked by agents.txt") {
		t.Errorf("error should mention agents.txt: %v", err)
	}
}

func TestDefaultFetcher_RobotsTxtCaching(t *testing.T) {
	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fetchCount++
			_, _ = fmt.Fprint(w, "User-agent: *\nAllow: /\n")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	f := NewDefaultFetcher(Options{
		Timeout:          5 * time.Second,
		RespectRobotsTxt: true,
	})

	// Two fetches should only hit robots.txt once (cached).
	_, err := f.Fetch(context.Background(), srv.URL+"/page1")
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	_, err = f.Fetch(context.Background(), srv.URL+"/page2")
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if fetchCount != 1 {
		t.Errorf("robots.txt fetched %d times, want 1 (should be cached)", fetchCount)
	}
}

func TestDefaultFetcher_RobotsTxtDisabledByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "fetched")
	}))
	defer srv.Close()

	// Default: robots.txt NOT respected.
	f := NewDefaultFetcher(Options{Timeout: 5 * time.Second})
	result, err := f.Fetch(context.Background(), srv.URL+"/blocked")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "fetched" {
		t.Errorf("content = %q, should have fetched despite robots.txt", result.Content)
	}
}

func TestDefaultFetcher_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		_, _ = fmt.Fprint(w, "slow")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	f := NewDefaultFetcher(Options{Timeout: 10 * time.Second})
	_, err := f.Fetch(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestChainFetcher_PrimarySuccess(t *testing.T) {
	primary := &mockFetcher{
		name: "primary",
		result: &FetchResult{
			Content: "Primary content that is definitely long enough to pass the empty content check threshold. This sentence ensures we are well above one hundred characters of meaningful text content.",
		},
	}
	fallback := &mockFetcher{name: "fallback"}

	c := NewChainFetcher(primary, fallback, nil)
	result, err := c.Fetch(context.Background(), "http://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if !strings.Contains(result.Content, "Primary content") {
		t.Errorf("should use primary result, got: %s", result.Content)
	}
	if fallback.called {
		t.Error("fallback should not have been called")
	}
}

func TestChainFetcher_FallbackOnEmpty(t *testing.T) {
	primary := &mockFetcher{
		name:   "primary",
		result: &FetchResult{Content: ""}, // empty = JS page
	}
	fallback := &mockFetcher{
		name: "fallback",
		result: &FetchResult{
			Content: "Fallback content that is definitely long enough to pass the empty content check threshold. This sentence ensures we are well above one hundred characters.",
		},
	}

	c := NewChainFetcher(primary, fallback, nil)
	result, err := c.Fetch(context.Background(), "http://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "Fallback content") {
		t.Errorf("should use fallback result, got: %s", result.Content)
	}
}

func TestChainFetcher_FallbackOnPrimaryError(t *testing.T) {
	primary := &mockFetcher{
		name: "primary",
		err:  fmt.Errorf("primary failed"),
	}
	fallback := &mockFetcher{
		name:   "fallback",
		result: &FetchResult{Content: "Fallback worked"},
	}

	c := NewChainFetcher(primary, fallback, nil)
	result, err := c.Fetch(context.Background(), "http://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Fallback worked" {
		t.Errorf("should use fallback, got: %s", result.Content)
	}
}

func TestChainFetcher_NoFallback(t *testing.T) {
	primary := &mockFetcher{
		name:   "primary",
		result: &FetchResult{Content: "ok"},
	}

	c := NewChainFetcher(primary, nil, nil)
	result, err := c.Fetch(context.Background(), "http://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "ok" {
		t.Errorf("content = %q, want %q", result.Content, "ok")
	}
}

func TestJinaFetcher_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	j := NewJinaFetcher(5*time.Second, nil)
	j.baseURL = srv.URL

	_, err := j.Fetch(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected error for 503 response from Jina")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention 503: %v", err)
	}
}

func TestJinaFetcher_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = fmt.Fprint(w, "Title: Example Page\nURL Source: https://example.com\n\n# Example\n\nThis is content.")
	}))
	defer srv.Close()

	j := NewJinaFetcher(5*time.Second, nil)
	j.baseURL = srv.URL

	result, err := j.Fetch(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Title != "Example Page" {
		t.Errorf("title = %q, want %q", result.Title, "Example Page")
	}
	if !strings.Contains(result.Content, "Example") {
		t.Errorf("content should contain 'Example': %s", result.Content)
	}
	if result.ContentType != "text/markdown" {
		t.Errorf("content_type = %q, want %q", result.ContentType, "text/markdown")
	}
}

func TestChainFetcher_BothFail(t *testing.T) {
	primary := &mockFetcher{
		name: "primary",
		err:  fmt.Errorf("primary failed"),
	}
	fallback := &mockFetcher{
		name: "fallback",
		err:  fmt.Errorf("fallback failed"),
	}

	c := NewChainFetcher(primary, fallback, nil)
	_, err := c.Fetch(context.Background(), "http://example.com")
	if err == nil {
		t.Fatal("expected error when both fetchers fail")
	}
}

func TestChainFetcher_FallbackFailReturnsPrimary(t *testing.T) {
	// When primary returns empty content and fallback fails,
	// the chain should return the primary result (not an error).
	primary := &mockFetcher{
		name:   "primary",
		result: &FetchResult{Content: "tiny"},
	}
	fallback := &mockFetcher{
		name: "fallback",
		err:  fmt.Errorf("fallback failed"),
	}

	c := NewChainFetcher(primary, fallback, nil)
	result, err := c.Fetch(context.Background(), "http://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "tiny" {
		t.Errorf("should return primary result, got: %q", result.Content)
	}
}

func TestJinaFetcher_Name(t *testing.T) {
	j := NewJinaFetcher(5*time.Second, nil)
	if j.Name() != "jina" {
		t.Errorf("name = %q, want %q", j.Name(), "jina")
	}
}

func TestExtractTitle(t *testing.T) {
	body := []byte(`<html><head><title>My Page Title</title></head><body></body></html>`)
	title := extractTitle(body)
	if title != "My Page Title" {
		t.Errorf("title = %q, want %q", title, "My Page Title")
	}
}

func TestExtractTitle_NoTitle(t *testing.T) {
	body := []byte(`<html><head></head><body>No title here</body></html>`)
	title := extractTitle(body)
	if title != "" {
		t.Errorf("title = %q, want empty", title)
	}
}

func TestExtractJinaTitle(t *testing.T) {
	if got := extractJinaTitle("Title: My Page\nURL Source: https://example.com\nContent here"); got != "My Page" {
		t.Errorf("extractJinaTitle = %q, want %q", got, "My Page")
	}
	if got := extractJinaTitle("No title prefix here\nJust content"); got != "" {
		t.Errorf("extractJinaTitle should return empty for missing title, got %q", got)
	}
	if got := extractJinaTitle(""); got != "" {
		t.Errorf("extractJinaTitle should return empty for empty string, got %q", got)
	}
}

func TestIsHTML(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"application/xhtml+xml", true},
		{"text/plain", false},
		{"application/json", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isHTML(tt.ct); got != tt.want {
			t.Errorf("isHTML(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

func TestLooksEmpty(t *testing.T) {
	if !looksEmpty("") {
		t.Error("empty string should look empty")
	}
	if !looksEmpty("   ") {
		t.Error("whitespace should look empty")
	}
	if !looksEmpty("short") {
		t.Error("very short string should look empty")
	}
	long := strings.Repeat("x", 200)
	if looksEmpty(long) {
		t.Error("200 chars should not look empty")
	}
}

// mockFetcher is a test double for Fetcher.
type mockFetcher struct {
	name   string
	result *FetchResult
	err    error
	called bool
}

func (m *mockFetcher) Fetch(_ context.Context, _ string) (*FetchResult, error) {
	m.called = true
	return m.result, m.err
}

func (m *mockFetcher) Name() string { return m.name }
