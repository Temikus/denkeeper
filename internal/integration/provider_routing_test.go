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

// Clearing a previously-set routing field through PATCH must remove it from
// the TOML too, not just from memory — otherwise the stale value resurrects on
// the next restart/reload. Regression test for the zero-value-skip persistence
// bug.
func TestProviderPatch_Routing_ClearRemovesStaleTOML(t *testing.T) {
	h := providerCrudHarness(t)
	withOpenRouterProvider(h)

	// Set an explicit order + custom TTL.
	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-or",
		map[string]any{"routing": map[string]any{
			"sticky": true, "sticky_ttl": "45m", "order": []string{"moonshotai"},
		}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("set PATCH status = %d: %s", rec.Code, rec.Body.String())
	}
	content, _ := os.ReadFile(h.ConfigPath())
	if toml := string(content); !strings.Contains(toml, "moonshotai") || !strings.Contains(toml, "45m") {
		t.Fatalf("setup did not persist order/ttl:\n%s", toml)
	}

	// Now clear the TTL and order (sticky stays on, others omitted/empty).
	rec = h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-or",
		map[string]any{"routing": map[string]any{"sticky": true}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear PATCH status = %d: %s", rec.Code, rec.Body.String())
	}

	// TOML must no longer carry the stale order/ttl values.
	content, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading TOML: %v", err)
	}
	toml := string(content)
	if strings.Contains(toml, "moonshotai") {
		t.Errorf("stale provider_order survived clear:\n%s", toml)
	}
	if strings.Contains(toml, "45m") {
		t.Errorf("stale provider_sticky_ttl survived clear:\n%s", toml)
	}

	// In-memory config must agree: order and TTL cleared, sticky still on.
	or := h.Config().LLM.OpenRouter
	if len(or.ProviderOrder) != 0 {
		t.Errorf("ProviderOrder = %v, want empty", or.ProviderOrder)
	}
	if or.ProviderStickyTTL != "" {
		t.Errorf("ProviderStickyTTL = %q, want empty", or.ProviderStickyTTL)
	}
	if or.ProviderSticky == nil || !*or.ProviderSticky {
		t.Errorf("ProviderSticky = %v, want true", or.ProviderSticky)
	}
}

// Reasoning persists as a single nested [llm.openrouter.reasoning] table that
// is replaced wholesale, so clearing a sub-field (here: effort) drops it from
// TOML rather than leaving a stale value — unlike the flat routing keys, which
// needed explicit deletion. This locks in that structural safety property.
func TestProviderPatch_Reasoning_ClearRemovesStaleTOML(t *testing.T) {
	h := providerCrudHarness(t)
	withOpenRouterProvider(h)

	// Set reasoning with an explicit effort level.
	rec := h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-or",
		map[string]any{"reasoning": map[string]any{"enabled": true, "effort": "high"}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("set PATCH status = %d: %s", rec.Code, rec.Body.String())
	}
	content, _ := os.ReadFile(h.ConfigPath())
	if !strings.Contains(string(content), "high") {
		t.Fatalf("setup did not persist effort:\n%s", string(content))
	}

	// Clear effort (enabled stays on, effort omitted).
	rec = h.Do(h.AuthedRequest("PATCH", "/api/v1/llm/providers/mock-or",
		map[string]any{"reasoning": map[string]any{"enabled": true}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear PATCH status = %d: %s", rec.Code, rec.Body.String())
	}

	content, err := os.ReadFile(h.ConfigPath())
	if err != nil {
		t.Fatalf("reading TOML: %v", err)
	}
	if strings.Contains(string(content), "high") {
		t.Errorf("stale reasoning effort survived clear:\n%s", string(content))
	}
	if got := h.Config().LLM.OpenRouter.Reasoning.Effort; got != "" {
		t.Errorf("in-memory Effort = %q, want empty", got)
	}
}
