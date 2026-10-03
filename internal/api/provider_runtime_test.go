package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

// fakeProviderRuntime records what the handlers push to the live set.
type fakeProviderRuntime struct {
	mu      sync.Mutex
	applied []config.ProviderInstanceConfig
	removed []string
}

func (f *fakeProviderRuntime) Apply(pc config.ProviderInstanceConfig, _ *config.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, pc)
	return nil
}

func (f *fakeProviderRuntime) Remove(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, name)
}

// providerTestServer returns a server over cfg with a real TOML file holding
// toml, so the handlers' persist-then-update path runs end to end.
func providerTestServer(t *testing.T, cfg *config.Config, toml string, rt ProviderRuntime) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "denkeeper.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Server{
		cfg:    cfg.API,
		deps:   Deps{Config: config.NewHolder(cfg), ConfigPath: path, Providers: rt},
		logger: testLogger(),
	}, path
}

func createProvider(t *testing.T, s *Server, body string) providerCreateResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleCreateLLMProvider(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body)
	}
	var resp providerCreateResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestCreateLLMProvider_AppliesLive(t *testing.T) {
	rt := &fakeProviderRuntime{}
	s, _ := providerTestServer(t, &config.Config{}, "", rt)

	resp := createProvider(t, s, `{"name":"anthropic","type":"anthropic","api_key":"sk-ant-x"}`)

	if resp.RestartRequired {
		t.Error("restart_required = true, want false when a runtime is wired")
	}
	if len(rt.applied) != 1 || rt.applied[0].Name != "anthropic" || rt.applied[0].APIKey != "sk-ant-x" {
		t.Errorf("applied = %+v, want the new anthropic instance", rt.applied)
	}
}

func TestCreateLLMProvider_FirstProviderBecomesDefault(t *testing.T) {
	s, path := providerTestServer(t, &config.Config{}, "", &fakeProviderRuntime{})

	resp := createProvider(t, s, `{"name":"anthropic","type":"anthropic","api_key":"sk-ant-x"}`)

	if !resp.Default {
		t.Error("default = false, want true for the first provider")
	}
	if got := s.appConfig().LLM.DefaultProvider; got != "anthropic" {
		t.Errorf("in-memory default_provider = %q, want anthropic", got)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `default_provider = 'anthropic'`) && !strings.Contains(string(data), `default_provider = "anthropic"`) {
		t.Errorf("TOML missing default_provider:\n%s", data)
	}
}

func TestCreateLLMProvider_ExistingDefaultKept(t *testing.T) {
	cfg := &config.Config{LLM: config.LLMConfig{
		DefaultProvider: "openrouter",
		Providers:       []config.ProviderInstanceConfig{{Name: "openrouter", Type: "openrouter", APIKey: "k"}},
	}}
	s, _ := providerTestServer(t, cfg, "[llm]\ndefault_provider = \"openrouter\"\n", &fakeProviderRuntime{})

	resp := createProvider(t, s, `{"name":"anthropic","type":"anthropic","api_key":"sk-ant-x"}`)

	if resp.Default || s.appConfig().LLM.DefaultProvider != "openrouter" {
		t.Errorf("default changed to %q (resp.default=%v), want openrouter kept", s.appConfig().LLM.DefaultProvider, resp.Default)
	}
}

func TestCreateLLMProvider_NoRuntime_RestartRequired(t *testing.T) {
	s, _ := providerTestServer(t, &config.Config{}, "", nil)

	resp := createProvider(t, s, `{"name":"anthropic","type":"anthropic","api_key":"sk-ant-x"}`)

	if !resp.RestartRequired {
		t.Error("restart_required = false, want true with no runtime wired")
	}
}

func TestPatchLLMProvider_ReplacesLive(t *testing.T) {
	rt := &fakeProviderRuntime{}
	cfg := &config.Config{LLM: config.LLMConfig{
		Providers: []config.ProviderInstanceConfig{{Name: "anthropic", Type: "anthropic", APIKey: "old"}},
	}}
	toml := "[[llm.providers]]\nname = \"anthropic\"\ntype = \"anthropic\"\napi_key = \"old\"\n"
	s, _ := providerTestServer(t, cfg, toml, rt)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/llm/providers/anthropic", strings.NewReader(`{"api_key":"new"}`))
	req.SetPathValue("name", "anthropic")
	rec := httptest.NewRecorder()
	s.handlePatchLLMProvider(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if len(rt.applied) != 1 || rt.applied[0].APIKey != "new" {
		t.Errorf("applied = %+v, want the instance rebuilt with the new key", rt.applied)
	}
	if !strings.Contains(rec.Body.String(), `"restart_required":false`) {
		t.Errorf("body = %s, want restart_required false", rec.Body)
	}
}

func TestDeleteLLMProvider_RemovesLive(t *testing.T) {
	rt := &fakeProviderRuntime{}
	cfg := &config.Config{LLM: config.LLMConfig{
		DefaultProvider: "openrouter",
		Providers: []config.ProviderInstanceConfig{
			{Name: "openrouter", Type: "openrouter", APIKey: "k"},
			{Name: "spare", Type: "anthropic", APIKey: "k"},
		},
	}}
	toml := "[llm]\ndefault_provider = \"openrouter\"\n\n" +
		"[[llm.providers]]\nname = \"openrouter\"\ntype = \"openrouter\"\napi_key = \"k\"\n\n" +
		"[[llm.providers]]\nname = \"spare\"\ntype = \"anthropic\"\napi_key = \"k\"\n"
	s, _ := providerTestServer(t, cfg, toml, rt)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/llm/providers/spare", nil)
	req.SetPathValue("name", "spare")
	rec := httptest.NewRecorder()
	s.handleDeleteLLMProvider(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if len(rt.removed) != 1 || rt.removed[0] != "spare" {
		t.Errorf("removed = %v, want [spare]", rt.removed)
	}
}
