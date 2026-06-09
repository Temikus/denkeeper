package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// modelListerProvider is a mock llm.Provider that also implements ModelLister,
// used to exercise the provider connection-test endpoint. chatErr drives the
// ChatCompletion probe used for OAuth providers; listErr drives ListModels /
// HealthCheck used for API-key providers. chatModel records the model id the
// probe was called with.
type modelListerProvider struct {
	models    []string
	listErr   error
	chatErr   error
	chatCalls int
	chatModel string
}

func (m *modelListerProvider) Name() string { return "anthropic" }
func (m *modelListerProvider) ChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	m.chatCalls++
	m.chatModel = req.Model
	if m.chatErr != nil {
		return nil, m.chatErr
	}
	return &llm.ChatResponse{}, nil
}
func (m *modelListerProvider) HealthCheck(_ context.Context) error { return m.listErr }
func (m *modelListerProvider) ListModels(_ context.Context) ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.models, nil
}

func writeTempConfigFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

func TestCreateLLMProvider_OAuth(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.ConfigPath = writeTempConfigFile(t, "[llm]\ndefault_provider = \"anthropic\"\n")
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{
		"name":        "claude-sub",
		"type":        "anthropic",
		"auth":        "oauth",
		"oauth_token": "sk-ant-oat01-secret",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}

	// In-memory config updated with oauth.
	var found *config.ProviderInstanceConfig
	for i := range srv.deps.Config.LLM.Providers {
		if srv.deps.Config.LLM.Providers[i].Name == "claude-sub" {
			found = &srv.deps.Config.LLM.Providers[i]
		}
	}
	if found == nil {
		t.Fatal("provider not added to in-memory config")
	}
	if !found.IsOAuth() || found.OAuthToken != "sk-ant-oat01-secret" {
		t.Errorf("provider = %+v, want oauth with token", found)
	}

	// Persisted to TOML.
	persisted, _ := os.ReadFile(deps.ConfigPath)
	if !strings.Contains(string(persisted), "sk-ant-oat01-secret") {
		t.Errorf("token not persisted to TOML:\n%s", persisted)
	}
}

func TestCreateLLMProvider_OAuthRequiresToken(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.ConfigPath = writeTempConfigFile(t, "[llm]\n")
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"name": "claude-sub", "type": "anthropic", "auth": "oauth"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "oauth_token is required") {
		t.Errorf("unexpected error body: %s", rec.Body.String())
	}
}

func TestCreateLLMProvider_OAuthRejectedForNonAnthropic(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.ConfigPath = writeTempConfigFile(t, "[llm]\n")
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{
		"name": "gw", "type": "openai", "auth": "oauth", "oauth_token": "x",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "only supported for anthropic") {
		t.Errorf("unexpected error body: %s", rec.Body.String())
	}
}

func TestGetLLMProviders_MasksOAuthToken(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "claude-sub", Type: "anthropic", Auth: config.AuthModeOAuth, OAuthToken: "sk-ant-oat01-secret"},
	}
	srv := New(cfg, deps, testLogger())

	req := authedRequest(http.MethodGet, "/api/v1/llm/providers")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	bodyStr := rec.Body.String()
	if strings.Contains(bodyStr, "sk-ant-oat01-secret") {
		t.Errorf("response leaked oauth token:\n%s", bodyStr)
	}

	var resp llmProvidersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	var p *providerInfo
	for i := range resp.Providers {
		if resp.Providers[i].Name == "claude-sub" {
			p = &resp.Providers[i]
		}
	}
	if p == nil {
		t.Fatal("claude-sub provider missing from response")
	}
	if p.Auth != "oauth" || !p.OAuthTokenSet || !p.Enabled {
		t.Errorf("provider info = %+v, want auth=oauth, token set, enabled", p)
	}
}

func TestPatchLLMProvider_SwitchToOAuth(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "anthropic", Type: "anthropic", APIKey: "sk-ant-key"},
	}
	deps.ConfigPath = writeTempConfigFile(t, "[[llm.providers]]\nname = \"anthropic\"\ntype = \"anthropic\"\napi_key = \"sk-ant-key\"\n")
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"auth": "oauth", "oauth_token": "sk-ant-oat01-new"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/llm/providers/anthropic", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	p := srv.deps.Config.LLM.Providers[0]
	if !p.IsOAuth() || p.OAuthToken != "sk-ant-oat01-new" {
		t.Errorf("provider = %+v, want oauth with new token", p)
	}
}

func TestPatchLLMProvider_OAuthOnNonAnthropicRejected(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "gw", Type: "openai", APIKey: "sk-x"},
	}
	deps.ConfigPath = writeTempConfigFile(t, "[llm]\n")
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"auth": "oauth", "oauth_token": "x"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/llm/providers/gw", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestTestLLMProvider_OAuthProbesMessagesAPI(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	// A default model the subscription may not have access to — the probe must
	// not depend on it.
	deps.Config.LLM.DefaultModel = "anthropic/claude-opus-4-1-20250805"
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "claude-sub", Type: "anthropic", Auth: config.AuthModeOAuth, OAuthToken: "tok"},
	}
	// The OAuth probe must exercise the Messages API (ChatCompletion), picking a
	// model the credential advertises (cheapest haiku tier), not the default.
	mock := &modelListerProvider{models: []string{"claude-opus-4-8", "claude-haiku-4-5", "claude-sonnet-4-6"}}
	deps.ProviderFactory = func(config.ProviderInstanceConfig) llm.Provider { return mock }
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers/claude-sub/test", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK     bool `json:"ok"`
		Models int  `json:"models"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK {
		t.Errorf("resp = %+v, want ok", resp)
	}
	if mock.chatCalls != 1 {
		t.Errorf("ChatCompletion calls = %d, want 1 (OAuth must probe the Messages API)", mock.chatCalls)
	}
	// Probe must use a credential-advertised model, preferring the haiku tier —
	// never the configured default.
	if mock.chatModel != "claude-haiku-4-5" {
		t.Errorf("probe model = %q, want %q (cheapest advertised, not the default)", mock.chatModel, "claude-haiku-4-5")
	}
	// The /v1/models count must not be surfaced for an OAuth probe.
	if resp.Models != 0 {
		t.Errorf("models = %d, want 0 (not surfaced for OAuth probe)", resp.Models)
	}
}

func TestTestLLMProvider_OAuthProbeFallsBackWhenNoModelList(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "claude-sub", Type: "anthropic", Auth: config.AuthModeOAuth, OAuthToken: "tok"},
	}
	// Empty model list — the probe falls back to a small default model rather
	// than failing or depending on config.
	mock := &modelListerProvider{models: nil}
	deps.ProviderFactory = func(config.ProviderInstanceConfig) llm.Provider { return mock }
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers/claude-sub/test", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if mock.chatModel != defaultProbeModel {
		t.Errorf("probe model = %q, want fallback %q", mock.chatModel, defaultProbeModel)
	}
}

func TestTestLLMProvider_APIKeyUsesListModels(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "claude-key", Type: "anthropic", APIKey: "sk-ant-x"},
	}
	mock := &modelListerProvider{models: []string{"claude-opus-4-8", "claude-sonnet-4-6"}}
	deps.ProviderFactory = func(config.ProviderInstanceConfig) llm.Provider { return mock }
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers/claude-key/test", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK     bool `json:"ok"`
		Models int  `json:"models"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.OK || resp.Models != 2 {
		t.Errorf("resp = %+v, want ok with 2 models", resp)
	}
	if mock.chatCalls != 0 {
		t.Errorf("ChatCompletion calls = %d, want 0 (API-key test must list models, not chat)", mock.chatCalls)
	}
}

func TestTestLLMProvider_Failure(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "claude-sub", Type: "anthropic", Auth: config.AuthModeOAuth, OAuthToken: "bad"},
	}
	deps.ProviderFactory = func(config.ProviderInstanceConfig) llm.Provider {
		return &modelListerProvider{chatErr: &llm.LLMError{StatusCode: 401, Message: "invalid token"}}
	}
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers/claude-sub/test", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid token") {
		t.Errorf("expected upstream message surfaced; body: %s", rec.Body.String())
	}
}

func TestTestLLMProvider_Override(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "claude-sub", Type: "anthropic", Auth: config.AuthModeOAuth, OAuthToken: "saved"},
	}
	var gotToken string
	deps.ProviderFactory = func(pc config.ProviderInstanceConfig) llm.Provider {
		gotToken = pc.OAuthToken
		return &modelListerProvider{models: []string{"m"}}
	}
	srv := New(cfg, deps, testLogger())

	body, _ := json.Marshal(map[string]any{"oauth_token": "unsaved-draft"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers/claude-sub/test", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if gotToken != "unsaved-draft" {
		t.Errorf("factory got token %q, want override 'unsaved-draft'", gotToken)
	}
}

func TestTestLLMProvider_Unavailable(t *testing.T) {
	cfg := testConfig(allScopesKey())
	deps := testDeps()
	deps.Config.LLM.Providers = []config.ProviderInstanceConfig{
		{Name: "claude-sub", Type: "anthropic", Auth: config.AuthModeOAuth, OAuthToken: "tok"},
	}
	// No ProviderFactory configured.
	srv := New(cfg, deps, testLogger())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers/claude-sub/test", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", rec.Code, rec.Body.String())
	}
}
