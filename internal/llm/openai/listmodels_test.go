package openai

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

	_, err := NewWithHTTPClient("bad", srv.URL, "", srv.Client()).ListModels(context.Background())
	var le *llm.LLMError
	if !errors.As(err, &le) || le.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *llm.LLMError with status 401", err)
	}
}
