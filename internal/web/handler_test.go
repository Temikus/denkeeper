package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_IndexServedAtRoot(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandler_SPAFallbackForUnknownPath(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/unknown/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// SPA fallback: unknown paths serve index.html with 200.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html for SPA fallback", ct)
	}
}

func TestHandler_NoCacheHeaderOnIndexHTML(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	cc := rec.Header().Get("Cache-Control")
	if cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache for index.html", cc)
	}
}

func TestHandler_NoCacheHeaderOnSPAFallback(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/some-svelte-route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	cc := rec.Header().Get("Cache-Control")
	if cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache for SPA fallback", cc)
	}
}

func TestHandler_ImmutableCacheOnHashedAsset(t *testing.T) {
	sub, _ := fs.Sub(distFS, "dist")
	entries, err := fs.ReadDir(sub, "assets")
	if err != nil || len(entries) == 0 {
		t.Skip("no hashed assets in dist — run 'just build-ui' first")
	}

	assetPath := "/assets/" + entries[0].Name()
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, assetPath, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d for asset %s", rec.Code, http.StatusOK, assetPath)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable for hashed asset %s", cc, assetPath)
	}
}
