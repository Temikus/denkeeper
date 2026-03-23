package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(keys ...config.APIKeyConfig) config.APIConfig {
	return config.APIConfig{
		Enabled: true,
		Listen:  ":0",
		Keys:    keys,
	}
}

func TestHealth_ReturnsOK(t *testing.T) {
	srv := New(testConfig(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if body == "" || body == "{}" {
		t.Error("expected non-empty JSON response")
	}
}

func TestRequireScope_NoAuthHeader(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testLogger())

	handler := srv.RequireScope("health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireScope_InvalidKey(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testLogger())

	handler := srv.RequireScope("health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-wrong-key")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireScope_ValidKeyWrongScope(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testLogger())

	handler := srv.RequireScope("chat", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-secret")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireScope_ValidKeyValidScope(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "test", Key: "dk-secret", Scopes: []string{"health", "chat"},
	})
	srv := New(cfg, testLogger())

	handler := srv.RequireScope("chat", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-secret")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequireScope_ContextContainsKeyName(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "my-key", Key: "dk-secret", Scopes: []string{"health"},
	})
	srv := New(cfg, testLogger())

	var gotName string
	handler := srv.RequireScope("health", func(w http.ResponseWriter, r *http.Request) {
		gotName, _ = r.Context().Value(keyNameKey).(string)
		writeJSON(w, http.StatusOK, nil)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer dk-secret")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if gotName != "my-key" {
		t.Errorf("context key name = %q, want my-key", gotName)
	}
}

func TestCORS_OriginAllowed(t *testing.T) {
	cfg := testConfig()
	cfg.CORSOrigins = []string{"https://dashboard.example.com"}
	srv := New(cfg, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://dashboard.example.com" {
		t.Errorf("CORS origin = %q, want https://dashboard.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_OriginNotAllowed(t *testing.T) {
	cfg := testConfig()
	cfg.CORSOrigins = []string{"https://dashboard.example.com"}
	srv := New(cfg, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS should not set header for disallowed origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_Preflight(t *testing.T) {
	cfg := testConfig()
	cfg.CORSOrigins = []string{"https://dashboard.example.com"}
	srv := New(cfg, testLogger())

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestCORS_NoneConfigured(t *testing.T) {
	srv := New(testConfig(), testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS header should not be set when no origins configured")
	}
}

func TestRateLimit_Enforced(t *testing.T) {
	cfg := testConfig(config.APIKeyConfig{
		Name: "limited", Key: "dk-limited", Scopes: []string{"health"},
	})
	cfg.RateLimit = 1.0 // 1 request per second
	srv := New(cfg, testLogger())

	handler := srv.RequireScope("health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	// First request should succeed (bucket starts full).
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.Header.Set("Authorization", "Bearer dk-limited")
	rec1 := httptest.NewRecorder()
	handler(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", rec1.Code, http.StatusOK)
	}

	// Second request immediately should be rate limited.
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set("Authorization", "Bearer dk-limited")
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want %d", rec2.Code, http.StatusTooManyRequests)
	}
}

func TestRecover_PanicHandled(t *testing.T) {
	cfg := testConfig()
	srv := New(cfg, testLogger())

	// Register a handler that panics behind the middleware stack.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})

	// Wrap with the same middleware stack as the server.
	handler := srv.middlewareRecover(srv.middlewareLogging(mux))

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
