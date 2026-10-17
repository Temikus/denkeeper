package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

func TestListModels_Unauthorized_ReturnsLLMError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := NewWithHTTPClient("bad", srv.URL, srv.Client()).ListModels(context.Background())
	var le *llm.LLMError
	if !errors.As(err, &le) || le.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *llm.LLMError with status 401", err)
	}
}

// modelsPage is trimmed from a live /v1/models response.
const modelsPage = `{"data":[
 {"type":"model","id":"claude-sonnet-5-5","display_name":"Claude Sonnet 5.5","max_input_tokens":1000000},
 {"type":"model","id":"claude-haiku-4-5-20251001","display_name":"Claude Haiku 4.5","max_input_tokens":200000}
],"has_more":false}`

func newModelsServer(t *testing.T, gotLimit *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		*gotLimit = r.URL.Query().Get("limit")
		_, _ = w.Write([]byte(modelsPage))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListModels_RequestsFullPage(t *testing.T) {
	var limit string
	srv := newModelsServer(t, &limit)

	got, err := NewWithHTTPClient("k", srv.URL, srv.Client()).ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 2 || got[0] != "claude-sonnet-5-5" {
		t.Errorf("models = %v", got)
	}
	if limit != "1000" {
		t.Errorf("limit = %q, want 1000", limit)
	}
}

func TestListModelDetails_DisplayNameAndTools(t *testing.T) {
	var limit string
	srv := newModelsServer(t, &limit)
	c := NewWithHTTPClient("k", srv.URL, srv.Client())
	c.name = "anthropic-work"

	got, err := c.ListModelDetails(context.Background())
	if err != nil {
		t.Fatalf("ListModelDetails: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	want := llm.ModelInfo{ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5", Provider: "anthropic-work", SupportsTools: true}
	if got[0] != want {
		t.Errorf("got[0] = %+v, want %+v", got[0], want)
	}
	if !got[1].SupportsTools || got[1].Name != "Claude Haiku 4.5" {
		t.Errorf("got[1] = %+v", got[1])
	}
}
