package api

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func newHeaderTestServer() *Server {
	return New(testConfig(), testDeps(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestSecurityHeaders_SetCSP asserts the dashboard CSP is emitted and that
// script-src stays locked to 'self' (the control that defeats the stored-XSS →
// same-origin RCE chain). 'unsafe-inline' must never leak into script-src.
func TestSecurityHeaders_SetCSP(t *testing.T) {
	srv := newHeaderTestServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := srv.middlewareSecurityHeaders(mux)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("expected Content-Security-Policy header, got none")
	}
	for _, want := range []string{
		"default-src 'self'", "script-src 'self'", "object-src 'none'",
		"base-uri 'self'", "frame-ancestors 'none'", "connect-src 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q; got %q", want, csp)
		}
	}
	// script-src must not permit inline scripts.
	if regexp.MustCompile(`script-src[^;]*'unsafe-inline'`).MatchString(csp) {
		t.Errorf("script-src must not allow 'unsafe-inline'; got %q", csp)
	}
}

// TestServeCallbackHTML_CSPHashMatchesScript guards against drift between the
// OAuth success page's inline auto-close script and the CSP hash that permits
// it: if the script changes, the constant must be regenerated or the browser
// will silently block the script.
func TestServeCallbackHTML_CSPHashMatchesScript(t *testing.T) {
	srv := newHeaderTestServer()
	rec := httptest.NewRecorder()
	srv.serveCallbackHTML(rec, true, "")

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, callbackAutoCloseScriptHash) {
		t.Fatalf("callback CSP missing script hash %q; got %q", callbackAutoCloseScriptHash, csp)
	}

	m := regexp.MustCompile(`(?s)<script>(.*?)</script>`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("no inline <script> found in success callback page")
	}
	sum := sha256.Sum256([]byte(m[1]))
	got := "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
	if got != callbackAutoCloseScriptHash {
		t.Errorf("inline script hash = %q, but callbackAutoCloseScriptHash = %q (update the constant)", got, callbackAutoCloseScriptHash)
	}
}
