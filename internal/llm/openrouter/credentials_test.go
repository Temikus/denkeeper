package openrouter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

// keyServer answers /key with 200 only for the key "good".
func keyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/key" {
			t.Errorf("path = %s, want /key", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"label":"x"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckCredentials_Valid(t *testing.T) {
	srv := keyServer(t)
	if err := NewWithHTTPClient("good", srv.URL, srv.Client()).CheckCredentials(context.Background()); err != nil {
		t.Fatalf("CheckCredentials: %v", err)
	}
}

func TestCheckCredentials_Invalid401(t *testing.T) {
	srv := keyServer(t)
	err := NewWithHTTPClient("bad", srv.URL, srv.Client()).CheckCredentials(context.Background())
	var le *llm.LLMError
	if !errors.As(err, &le) || le.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *llm.LLMError with status 401", err)
	}
}

func TestListModels_ErrorStatus_ReturnsLLMError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := NewWithHTTPClient("k", srv.URL, srv.Client()).ListModels(context.Background())
	var le *llm.LLMError
	if !errors.As(err, &le) || le.StatusCode != http.StatusForbidden {
		t.Fatalf("err = %v, want *llm.LLMError with status 403", err)
	}
}
