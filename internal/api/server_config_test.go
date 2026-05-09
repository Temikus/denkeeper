package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func testDepsWithServerConfig() Deps {
	deps := testDeps()
	deps.Config.API = config.APIConfig{
		Listen:                   ":8443",
		TLS:                      true,
		CORSOrigins:              []string{"https://example.com"},
		RateLimit:                100,
		WebSocketEnabled:         boolPtr(true),
		WebSocketMaxConnections:  50,
		WebSocketReplayBufferTTL: "5m",
		ExternalURL:              "https://den.example.com",
	}
	deps.Version = "1.2.3"
	deps.Commit = "abc1234"
	deps.BuildDate = "2025-01-01"
	return deps
}

func TestGetServerConfig_Success(t *testing.T) {
	deps := testDepsWithServerConfig()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/server/config")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp serverConfigResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.ExternalURL != "https://den.example.com" {
		t.Errorf("external_url = %q, want https://den.example.com", resp.ExternalURL)
	}
	if resp.Listen != ":8443" {
		t.Errorf("listen = %q, want :8443", resp.Listen)
	}
	if !resp.TLS {
		t.Error("tls = false, want true")
	}
	if resp.RateLimit != 100 {
		t.Errorf("rate_limit = %v, want 100", resp.RateLimit)
	}
	if !resp.WebSocketEnabled {
		t.Error("websocket_enabled = false, want true")
	}
	if resp.WebSocketMaxConnections != 50 {
		t.Errorf("websocket_max_connections = %d, want 50", resp.WebSocketMaxConnections)
	}
	if resp.Version != "1.2.3" {
		t.Errorf("version = %q, want 1.2.3", resp.Version)
	}
	if resp.Commit != "abc1234" {
		t.Errorf("commit = %q, want abc1234", resp.Commit)
	}
	if resp.BuildDate != "2025-01-01" {
		t.Errorf("build_date = %q, want 2025-01-01", resp.BuildDate)
	}
	if resp.GoVersion == "" {
		t.Error("go_version should not be empty")
	}
}

func TestGetServerConfig_EmptyCORSOrigins(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/server/config")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	origins, ok := resp["cors_origins"].([]any)
	if !ok {
		t.Fatal("cors_origins is not an array")
	}
	if len(origins) != 0 {
		t.Errorf("cors_origins = %v, want empty array", origins)
	}
}

func TestPatchServerConfig_ExternalURL(t *testing.T) {
	deps := testDepsWithServerConfig()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body, _ := json.Marshal(map[string]any{"external_url": "https://new.example.com"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/server/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if deps.Config.API.ExternalURL != "https://new.example.com" {
		t.Errorf("in-memory external_url = %q, want https://new.example.com", deps.Config.API.ExternalURL)
	}
}

func TestPatchServerConfig_ClearExternalURL(t *testing.T) {
	deps := testDepsWithServerConfig()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body, _ := json.Marshal(map[string]any{"external_url": ""})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/server/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if deps.Config.API.ExternalURL != "" {
		t.Errorf("in-memory external_url = %q, want empty", deps.Config.API.ExternalURL)
	}
}

func TestPatchServerConfig_InvalidURL(t *testing.T) {
	deps := testDepsWithServerConfig()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body, _ := json.Marshal(map[string]any{"external_url": "not-a-url"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/server/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestPatchServerConfig_InvalidJSON(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithServerConfig(), testLogger())

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/server/config", bytes.NewReader([]byte("{")))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdateAPIConfig_Persistence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// Write initial config with an existing [api] section.
	initial := []byte("[api]\nlisten = \":8080\"\n")
	if err := os.WriteFile(cfgPath, initial, 0644); err != nil {
		t.Fatal(err)
	}

	// Update external_url via the config writer.
	changes := map[string]any{"external_url": "https://den.example.com"}
	if err := config.UpdateAPIConfig(cfgPath, changes); err != nil {
		t.Fatalf("UpdateAPIConfig: %v", err)
	}

	// Re-read and verify both the old and new fields are present.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !bytes.Contains(data, []byte("external_url")) {
		t.Errorf("persisted config missing external_url; got:\n%s", content)
	}
	if !bytes.Contains(data, []byte("listen")) {
		t.Errorf("persisted config lost listen field; got:\n%s", content)
	}
}

func TestReloadConfig_Success(t *testing.T) {
	deps := testDepsWithServerConfig()
	called := false
	deps.ReloadFunc = func() error {
		called = true
		return nil
	}
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := authedRequest(http.MethodPost, "/api/v1/server/reload")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !called {
		t.Error("ReloadFunc was not called")
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "reloaded" {
		t.Errorf("status = %q, want reloaded", resp["status"])
	}
}

func TestReloadConfig_Error(t *testing.T) {
	deps := testDepsWithServerConfig()
	deps.ReloadFunc = func() error {
		return errors.New("parse error")
	}
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := authedRequest(http.MethodPost, "/api/v1/server/reload")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

func TestReloadConfig_Unavailable(t *testing.T) {
	deps := testDepsWithServerConfig()
	// ReloadFunc is nil by default.
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := authedRequest(http.MethodPost, "/api/v1/server/reload")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRestartProcess_Success(t *testing.T) {
	deps := testDepsWithServerConfig()
	called := false
	deps.RestartFunc = func() error {
		called = true
		return nil
	}
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := authedRequest(http.MethodPost, "/api/v1/server/restart")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "restarting" {
		t.Errorf("status = %q, want restarting", resp["status"])
	}
	// RestartFunc runs asynchronously after a delay, so we don't assert called=true
	// in a unit test — just verify the response is correct.
	_ = called
}

func TestRestartProcess_Unavailable(t *testing.T) {
	deps := testDepsWithServerConfig()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := authedRequest(http.MethodPost, "/api/v1/server/restart")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
