package webfetch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// JinaFetcher uses the Jina Reader API (r.jina.ai) to fetch URLs and convert
// them to Markdown. Jina handles JavaScript rendering, making it suitable for
// JS-heavy pages that the DefaultFetcher cannot process.
type JinaFetcher struct {
	client  *http.Client
	logger  *slog.Logger
	baseURL string // overridable for testing; default "https://r.jina.ai"
}

// NewJinaFetcher creates a Jina Reader fetcher.
func NewJinaFetcher(timeout time.Duration, logger *slog.Logger) *JinaFetcher {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &JinaFetcher{
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
		baseURL: "https://r.jina.ai",
	}
}

// Name returns the fetcher identifier.
func (j *JinaFetcher) Name() string { return "jina" }

// Fetch retrieves a URL via Jina Reader and returns Markdown content.
func (j *JinaFetcher) Fetch(ctx context.Context, rawURL string) (*FetchResult, error) {
	jinaURL := j.baseURL + "/" + rawURL

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jinaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating jina request: %w", err)
	}
	req.Header.Set("Accept", "text/markdown")
	req.Header.Set("X-Respond-With", "markdown")

	resp, err := j.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jina fetch %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("jina returned HTTP %d for %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit for Jina
	if err != nil {
		return nil, fmt.Errorf("reading jina response: %w", err)
	}

	content := string(body)

	// Jina returns markdown with a title line like "Title: ..." at the top.
	title := extractJinaTitle(content)

	return &FetchResult{
		URL:          rawURL,
		Title:        title,
		Content:      content,
		ContentType:  "text/markdown",
		BytesFetched: len(body),
	}, nil
}

// extractJinaTitle attempts to extract a title from Jina's markdown output.
// Jina often prefixes output with "Title: <title>\nURL Source: <url>\n".
func extractJinaTitle(content string) string {
	for _, line := range strings.SplitN(content, "\n", 5) {
		if strings.HasPrefix(line, "Title: ") {
			return strings.TrimPrefix(line, "Title: ")
		}
	}
	return ""
}
