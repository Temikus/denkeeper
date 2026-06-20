//go:build integration

package integration

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

// withOpenRouterProvider appends an OpenRouter provider to the harness so the
// OpenRouter-only routing endpoints have a valid target.
func withOpenRouterProvider(h *Harness) {
	h.Config().LLM.Providers = append(h.Config().LLM.Providers,
		config.ProviderInstanceConfig{Name: "mock-or", Type: "openrouter", APIKey: "sk-or"})
}

func openRouterProvider(t *testing.T, h *Harness) map[string]any {
	t.Helper()
	rec := h.Do(h.AuthedRequest("GET", "/api/v1/llm/providers", nil))
	var body map[string]any
	DecodeJSON(t, rec, &body)
	for _, raw := range body["providers"].([]any) {
		p := raw.(map[string]any)
		if p["name"] == "mock-or" {
			return p
		}
	}
	t.Fatal("mock-or provider not found in GET response")
	return nil
}

func TestProviderPatch_Routing_RoundTrip(t *testing.T) {
	h := providerCrudHarness(t)
	withOpenRouterProvider(h)

	// Sticky routing defaults to on with a nil TTL field; disable it explicitly.
	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-or",
		map[string]any{"routing": map[string]any{"sticky": false, "sticky_ttl": "30m"}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", rec.Code, rec.Body.String())
	}

	p := openRouterProvider(t, h)
	routing, ok := p["routing"].(map[string]any)
	if !ok {
		t.Fatalf("routing missing or wrong type: %v", p["routing"])
	}
	if routing["sticky"] != false {
		t.Errorf("routing.sticky = %v, want false", routing["sticky"])
	}
	if routing["sticky_ttl"] != "30m" {
		t.Errorf("routing.sticky_ttl = %v, want 30m", routing["sticky_ttl"])
	}

	// Confirm it reached the in-memory config the provider client reads from.
	or := h.Config().LLM.OpenRouter
	if or.ProviderSticky == nil || *or.ProviderSticky {
		t.Errorf("ProviderSticky = %v, want false", or.ProviderSticky)
	}
	if or.ProviderStickyTTL != "30m" {
		t.Errorf("ProviderStickyTTL = %q, want 30m", or.ProviderStickyTTL)
	}
}

func TestProviderPatch_Routing_RejectsBadTTL(t *testing.T) {
	h := providerCrudHarness(t)
	withOpenRouterProvider(h)

	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-or",
		map[string]any{"routing": map[string]any{"sticky_ttl": "not-a-duration"}}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestProviderPatch_Routing_RejectedForNonOpenRouter(t *testing.T) {
	h := providerCrudHarness(t)

	// mock-existing is an openai provider — routing is OpenRouter-only.
	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-existing",
		map[string]any{"routing": map[string]any{"sticky": true}}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestProviderPatch_Routing_PersistsToTOML(t *testing.T) {
	h := providerCrudHarness(t)
	withOpenRouterProvider(h)

	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-or",
		map[string]any{"routing": map[string]any{"sticky": false, "sticky_ttl": "45m"}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", rec.Code, rec.Body.String())
	}

	content, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading TOML: %v", err)
	}
	toml := string(content)
	if !strings.Contains(toml, "provider_sticky") {
		t.Errorf("provider_sticky not found in TOML:\n%s", toml)
	}
	if !strings.Contains(toml, "45m") {
		t.Errorf("provider_sticky_ttl=45m not found in TOML:\n%s", toml)
	}
}
