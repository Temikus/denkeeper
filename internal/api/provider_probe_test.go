package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

const probeKey = "sk-ant-secret-1234567890"

// anthropicUpstream answers /v1/models with two models for probeKey and 401
// for anything else.
func anthropicUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != probeKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad key ` + r.Header.Get("x-api-key") + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-b"},{"id":"claude-a"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func probe(t *testing.T, s *Server, body string) (int, providerTestResponse, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleTestLLMProvider(rec, httptest.NewRequest(http.MethodPost, "/api/v1/llm/providers/test", strings.NewReader(body)))
	raw := rec.Body.String()
	var resp providerTestResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, resp, raw
}

func probeServer(cfg *config.Config) *Server {
	return &Server{deps: Deps{Config: config.NewHolder(cfg)}, logger: testLogger()}
}

func TestProbeProvider_OK(t *testing.T) {
	up := anthropicUpstream(t)
	code, resp, _ := probe(t, probeServer(&config.Config{}),
		`{"type":"anthropic","api_key":"`+probeKey+`","base_url":"`+up.URL+`"}`)

	if code != http.StatusOK || resp.Status != "ok" {
		t.Fatalf("code=%d resp=%+v, want 200 ok", code, resp)
	}
	if resp.ModelCount != 2 || strings.Join(resp.Models, ",") != "claude-a,claude-b" {
		t.Errorf("models = %v (count %d), want sorted [claude-a claude-b]", resp.Models, resp.ModelCount)
	}
}

func TestProbeProvider_Rejected(t *testing.T) {
	up := anthropicUpstream(t)
	code, resp, _ := probe(t, probeServer(&config.Config{}),
		`{"type":"anthropic","api_key":"sk-ant-wrong","base_url":"`+up.URL+`"}`)

	// 200, not 401: the web client treats a 401 as its own session expiring.
	if code != http.StatusOK || resp.Status != "rejected" {
		t.Fatalf("code=%d resp=%+v, want 200 rejected", code, resp)
	}
	if !strings.Contains(resp.Message, "Anthropic says this key is not valid") {
		t.Errorf("message = %q", resp.Message)
	}
}

func TestProbeProvider_BodyNeverContainsKey(t *testing.T) {
	up := anthropicUpstream(t) // echoes the bad key in its error body
	_, _, raw := probe(t, probeServer(&config.Config{}),
		`{"type":"anthropic","api_key":"sk-ant-wrong-and-secret","base_url":"`+up.URL+`"}`)

	if strings.Contains(raw, "sk-ant-wrong-and-secret") {
		t.Fatalf("response leaked the key: %s", raw)
	}
}

func TestProbeProvider_Unreachable(t *testing.T) {
	up := anthropicUpstream(t)
	url := up.URL
	up.Close()

	code, resp, _ := probe(t, probeServer(&config.Config{}),
		`{"type":"anthropic","api_key":"`+probeKey+`","base_url":"`+url+`"}`)
	if code != http.StatusOK || resp.Status != "unreachable" {
		t.Fatalf("code=%d resp=%+v, want 200 unreachable", code, resp)
	}
}

func TestProbeProvider_OllamaNoModels(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer up.Close()

	_, resp, _ := probe(t, probeServer(&config.Config{}), `{"type":"ollama","base_url":"`+up.URL+`"}`)
	if resp.Status != "ok" || !strings.Contains(resp.Message, "ollama pull") {
		t.Errorf("resp = %+v, want ok with a pull hint", resp)
	}
}

func TestProbeProvider_StoredKeyByName(t *testing.T) {
	up := anthropicUpstream(t)
	cfg := &config.Config{LLM: config.LLMConfig{Providers: []config.ProviderInstanceConfig{
		{Name: "anthropic", Type: "anthropic", APIKey: probeKey, BaseURL: up.URL},
	}}}

	_, resp, _ := probe(t, probeServer(cfg), `{"name":"anthropic"}`)
	if resp.Status != "ok" {
		t.Errorf("resp = %+v, want ok using the stored key", resp)
	}
}

func TestProbeProvider_UnknownName404(t *testing.T) {
	code, _, _ := probe(t, probeServer(&config.Config{}), `{"name":"nope"}`)
	if code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", code)
	}
}

func TestProbeProvider_MissingKey400(t *testing.T) {
	code, _, _ := probe(t, probeServer(&config.Config{}), `{"type":"anthropic"}`)
	if code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", code)
	}
}

func TestProbeProvider_DoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer elsewhere.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v1/models", http.StatusFound)
	}))
	defer up.Close()

	_, resp, _ := probe(t, probeServer(&config.Config{}),
		`{"type":"anthropic","api_key":"`+probeKey+`","base_url":"`+up.URL+`"}`)
	if hits.Load() != 0 {
		t.Fatal("probe followed a redirect, which would carry the key to another host")
	}
	if resp.Status == "ok" {
		t.Errorf("resp = %+v, want a failure for a redirect", resp)
	}
}

func TestProbeProvider_StoredKeyIgnoresOverrideURL(t *testing.T) {
	up := anthropicUpstream(t)
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" {
			leaked.Store(true)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer other.Close()
	cfg := &config.Config{LLM: config.LLMConfig{Providers: []config.ProviderInstanceConfig{
		{Name: "anthropic", Type: "anthropic", APIKey: probeKey, BaseURL: up.URL},
	}}}

	_, resp, _ := probe(t, probeServer(cfg), `{"name":"anthropic","base_url":"`+other.URL+`"}`)
	if leaked.Load() {
		t.Fatal("stored key was sent to a caller-supplied base_url")
	}
	if resp.Status != "ok" {
		t.Errorf("resp = %+v, want ok against the stored URL", resp)
	}
}

func TestProbeProvider_OpenRouterBaseURL400(t *testing.T) {
	code, _, _ := probe(t, probeServer(&config.Config{}),
		`{"type":"openrouter","api_key":"k","base_url":"https://example.com"}`)
	if code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400: openrouter ignores base_url", code)
	}
}
