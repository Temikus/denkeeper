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

func TestNew_EachType_NamedAfterInstance(t *testing.T) {
	for _, typ := range []string{"anthropic", "openai", "openrouter", "ollama"} {
		t.Run(typ, func(t *testing.T) {
			p, err := New(config.ProviderInstanceConfig{Name: "my-" + typ, Type: typ, APIKey: "k"}, config.OpenRouterConfig{}, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if p.Name() != "my-"+typ {
				t.Errorf("Name() = %q, want my-%s", p.Name(), typ)
			}
		})
	}
}

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
	models, err := p.(llm.ModelLister).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || tr.n.Load() != 1 {
		t.Errorf("models=%v requests through override=%d; want 1 model via the injected client", models, tr.n.Load())
	}
}
