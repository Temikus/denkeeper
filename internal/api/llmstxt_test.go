package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

func TestLLMsTxt_NoAuth(t *testing.T) {
	s := &Server{
		cfg:    config.APIConfig{},
		logger: testLogger(),
	}

	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	rec := httptest.NewRecorder()
	s.handleLLMsTxt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", ct)
	}

	body := rec.Body.String()

	for _, want := range []string{
		"/api/v1/openapi.json",
		"POST /api/v1/chat",
		"# Denkeeper",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}

	if strings.Contains(body, "## Agents") {
		t.Error("body should not contain Agents section when Dispatcher is nil")
	}
}

func TestLLMsTxt_IncludesBaseURL(t *testing.T) {
	s := &Server{
		cfg:    config.APIConfig{ExternalURL: "https://my.denkeeper.example.com"},
		logger: testLogger(),
	}

	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	rec := httptest.NewRecorder()
	s.handleLLMsTxt(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "https://my.denkeeper.example.com") {
		t.Error("body should contain the configured external URL")
	}
}

func TestLLMsTxt_OmitsBaseURLWhenUnset(t *testing.T) {
	s := &Server{
		cfg:    config.APIConfig{},
		logger: testLogger(),
	}

	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	rec := httptest.NewRecorder()
	s.handleLLMsTxt(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "## Base URL") {
		t.Error("body should not contain Base URL section when ExternalURL is empty")
	}
}
