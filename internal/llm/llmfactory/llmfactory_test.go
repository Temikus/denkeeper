package llmfactory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// assertBuilds checks New builds typ under the instance's own name.
func assertBuilds(t *testing.T, typ string) {
	t.Helper()
	p, err := New(config.ProviderInstanceConfig{Name: "my-" + typ, Type: typ, APIKey: "k"}, config.OpenRouterConfig{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "my-"+typ {
		t.Errorf("Name() = %q, want my-%s", p.Name(), typ)
	}
}

func TestNew_Anthropic(t *testing.T)  { assertBuilds(t, "anthropic") }
func TestNew_OpenAI(t *testing.T)     { assertBuilds(t, "openai") }
func TestNew_OpenRouter(t *testing.T) { assertBuilds(t, "openrouter") }
func TestNew_Ollama(t *testing.T)     { assertBuilds(t, "ollama") }

func TestNew_UnknownType(t *testing.T) {
	if _, err := New(config.ProviderInstanceConfig{Name: "x", Type: "nope"}, config.OpenRouterConfig{}, nil); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

// countingTransport counts requests that pass through the injected client.
type countingTransport struct{ n atomic.Int32 }

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

func TestNew_HTTPClientOverride_IsUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-x"}]}`))
	}))
	defer srv.Close()

	tr := &countingTransport{}
	p, err := New(config.ProviderInstanceConfig{Name: "a", Type: "anthropic", APIKey: "k", BaseURL: srv.URL},
		config.OpenRouterConfig{}, &http.Client{Transport: tr})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lister, ok := p.(llm.ModelLister)
	if !ok {
		t.Fatal("anthropic client does not list models")
	}
	models, err := lister.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || tr.n.Load() != 1 {
		t.Errorf("models=%v requests through override=%d; want 1 model via the injected client", models, tr.n.Load())
	}
}

func TestNew_OpenRouterHonoursBaseURL(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gw/models" {
			hits.Add(1)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-x"}]}`))
	}))
	defer srv.Close()

	p, err := New(config.ProviderInstanceConfig{Name: "or", Type: "openrouter", APIKey: "k", BaseURL: srv.URL + "/gw"},
		config.OpenRouterConfig{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	lister, ok := p.(llm.ModelLister)
	if !ok {
		t.Fatal("openrouter client does not list models")
	}
	models, err := lister.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if hits.Load() != 1 || len(models) != 1 {
		t.Errorf("hits=%d models=%v; want one request to the base_url's /models", hits.Load(), models)
	}
}
