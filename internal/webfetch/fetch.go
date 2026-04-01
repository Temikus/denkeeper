// Package webfetch provides URL content fetching with HTML-to-Markdown conversion.
// It supports configurable timeouts, size limits, robots.txt/agents.txt compliance,
// and optional enhanced fetchers (e.g. Jina Reader) for JS-heavy pages.
package webfetch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/temoto/robotstxt"
	"golang.org/x/net/html"
)

// FetchResult holds the fetched and converted content from a URL.
type FetchResult struct {
	URL          string `json:"url"`
	Title        string `json:"title"`
	Content      string `json:"content"` // Markdown
	ContentType  string `json:"content_type"`
	BytesFetched int    `json:"bytes_fetched"`
}

// Fetcher retrieves content from a URL and returns it as Markdown.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*FetchResult, error)
	Name() string
}

// Options configures a DefaultFetcher.
type Options struct {
	Timeout          time.Duration
	MaxSizeBytes     int64
	UserAgent        string
	RespectRobotsTxt bool
	RespectAgentsTxt bool
	Logger           *slog.Logger
}

// DefaultFetcher fetches URLs via HTTP and converts HTML to Markdown.
type DefaultFetcher struct {
	client    *http.Client
	opts      Options
	robotsBot string // User-agent name for robots.txt lookups

	// robots.txt / agents.txt cache: domain → (data, fetchTime)
	robotsMu    sync.RWMutex
	robotsCache map[string]*robotsCacheEntry
	agentsMu    sync.RWMutex
	agentsCache map[string]*agentsCacheEntry
}

type robotsCacheEntry struct {
	data      *robotstxt.RobotsData
	fetchedAt time.Time
}

type agentsCacheEntry struct {
	data      *robotstxt.RobotsData // agents.txt uses same syntax
	fetchedAt time.Time
}

const robotsCacheTTL = 1 * time.Hour

// NewDefaultFetcher creates a fetcher with the given options.
func NewDefaultFetcher(opts Options) *DefaultFetcher {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "Denkeeper/1.0 (+https://denkeeper.io)"
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxSizeBytes == 0 {
		opts.MaxSizeBytes = 5 * 1024 * 1024 // 5MB
	}

	return &DefaultFetcher{
		client: &http.Client{
			Timeout: opts.Timeout,
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects (max 5)")
				}
				return nil
			},
		},
		opts:        opts,
		robotsBot:   "Denkeeper",
		robotsCache: make(map[string]*robotsCacheEntry),
		agentsCache: make(map[string]*agentsCacheEntry),
	}
}

// Name returns the fetcher identifier.
func (f *DefaultFetcher) Name() string { return "default" }

// Fetch retrieves a URL and converts its content to Markdown.
func (f *DefaultFetcher) Fetch(ctx context.Context, rawURL string) (*FetchResult, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q: only http and https are supported", parsed.Scheme)
	}

	// Check robots.txt / agents.txt if configured.
	if f.opts.RespectRobotsTxt {
		if blocked, err := f.isBlockedByRobots(ctx, parsed); err != nil {
			f.opts.Logger.Warn("robots.txt check failed, proceeding", "url", rawURL, "error", err)
		} else if blocked {
			return nil, fmt.Errorf("blocked by robots.txt: %s", rawURL)
		}
	}
	if f.opts.RespectAgentsTxt {
		if blocked, err := f.isBlockedByAgents(ctx, parsed); err != nil {
			f.opts.Logger.Warn("agents.txt check failed, proceeding", "url", rawURL, "error", err)
		} else if blocked {
			return nil, fmt.Errorf("blocked by agents.txt: %s", rawURL)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", f.opts.UserAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain, */*;q=0.8")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rawURL)
	}

	// Read body with size limit.
	limited := io.LimitReader(resp.Body, f.opts.MaxSizeBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if int64(len(body)) > f.opts.MaxSizeBytes {
		body = body[:f.opts.MaxSizeBytes]
	}

	contentType := resp.Header.Get("Content-Type")
	result := &FetchResult{
		URL:          resp.Request.URL.String(), // final URL after redirects
		ContentType:  contentType,
		BytesFetched: len(body),
	}

	if isHTML(contentType) {
		result.Title = extractTitle(body)
		md, convErr := htmltomd.ConvertString(string(body))
		if convErr != nil {
			// Fallback: return raw text content
			f.opts.Logger.Warn("HTML-to-Markdown conversion failed, returning raw text", "url", rawURL, "error", convErr)
			result.Content = string(body)
		} else {
			result.Content = md
		}
	} else {
		// Non-HTML content: return as-is (plain text, JSON, etc.)
		result.Content = string(body)
	}

	return result, nil
}

// isHTML checks if the content type indicates HTML.
func isHTML(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml+xml")
}

// extractTitle parses HTML and extracts the <title> text.
func extractTitle(body []byte) string {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return ""
	}
	return findTitle(doc)
}

func findTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		if n.FirstChild != nil {
			return strings.TrimSpace(n.FirstChild.Data)
		}
		return ""
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := findTitle(c); t != "" {
			return t
		}
	}
	return ""
}

// isBlockedByRobots checks whether the URL is disallowed by the site's robots.txt.
func (f *DefaultFetcher) isBlockedByRobots(ctx context.Context, u *url.URL) (bool, error) {
	domain := u.Scheme + "://" + u.Host
	data, err := f.getRobotsData(ctx, domain)
	if err != nil {
		return false, err
	}
	if data == nil {
		return false, nil // no robots.txt = allowed
	}
	return !data.TestAgent(u.Path, f.robotsBot), nil
}

func (f *DefaultFetcher) getRobotsData(ctx context.Context, domain string) (*robotstxt.RobotsData, error) {
	f.robotsMu.RLock()
	entry, ok := f.robotsCache[domain]
	f.robotsMu.RUnlock()

	if ok && time.Since(entry.fetchedAt) < robotsCacheTTL {
		return entry.data, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, domain+"/robots.txt", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.opts.UserAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data *robotstxt.RobotsData
	if resp.StatusCode == http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512*1024)) // 512KB limit
		if readErr != nil {
			return nil, readErr
		}
		data, err = robotstxt.FromBytes(body)
		if err != nil {
			return nil, fmt.Errorf("parsing robots.txt: %w", err)
		}
	}
	// 404 or other errors → treat as no robots.txt (data = nil, allowed)

	f.robotsMu.Lock()
	f.robotsCache[domain] = &robotsCacheEntry{data: data, fetchedAt: time.Now()}
	f.robotsMu.Unlock()

	return data, nil
}

// isBlockedByAgents checks whether the URL is disallowed by the site's agents.txt.
// agents.txt uses the same format as robots.txt (proposed standard for AI agents).
func (f *DefaultFetcher) isBlockedByAgents(ctx context.Context, u *url.URL) (bool, error) {
	domain := u.Scheme + "://" + u.Host
	data, err := f.getAgentsData(ctx, domain)
	if err != nil {
		return false, err
	}
	if data == nil {
		return false, nil
	}
	return !data.TestAgent(u.Path, f.robotsBot), nil
}

func (f *DefaultFetcher) getAgentsData(ctx context.Context, domain string) (*robotstxt.RobotsData, error) {
	f.agentsMu.RLock()
	entry, ok := f.agentsCache[domain]
	f.agentsMu.RUnlock()

	if ok && time.Since(entry.fetchedAt) < robotsCacheTTL {
		return entry.data, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, domain+"/agents.txt", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.opts.UserAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var data *robotstxt.RobotsData
	if resp.StatusCode == http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		if readErr != nil {
			return nil, readErr
		}
		data, err = robotstxt.FromBytes(body)
		if err != nil {
			return nil, fmt.Errorf("parsing agents.txt: %w", err)
		}
	}

	f.agentsMu.Lock()
	f.agentsCache[domain] = &agentsCacheEntry{data: data, fetchedAt: time.Now()}
	f.agentsMu.Unlock()

	return data, nil
}
