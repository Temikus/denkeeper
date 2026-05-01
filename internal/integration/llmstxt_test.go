//go:build integration

package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLLMsTxt_NoAuthRequired(t *testing.T) {
	h := NewHarness(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"# Denkeeper",
		"POST /api/v1/chat",
		"/api/v1/openapi.json",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestLLMsTxt_ListsAgents(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		Agents: []agentSetup{
			{Name: "alpha", Tier: "autonomous", Description: "First agent"},
			{Name: "beta", Tier: "supervised", Description: "Second agent"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	rec := h.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "## Agents") {
		t.Error("body missing Agents section")
	}
	if !strings.Contains(body, "alpha") {
		t.Error("body missing agent alpha")
	}
	if !strings.Contains(body, "First agent") {
		t.Error("body missing alpha description")
	}
	if !strings.Contains(body, "beta") {
		t.Error("body missing agent beta")
	}
}
