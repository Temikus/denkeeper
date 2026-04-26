//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

func TestModels_List(t *testing.T) {
	h := NewHarness(t, &HarnessOpts{
		ModelLister: func(_ context.Context) []string {
			return []string{"test-model-a", "test-model-b"}
		},
	})

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body struct {
		Models []string `json:"models"`
	}
	DecodeJSON(t, rec, &body)
	if len(body.Models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(body.Models))
	}
	if body.Models[0] != "test-model-a" || body.Models[1] != "test-model-b" {
		t.Errorf("models = %v, want [test-model-a test-model-b]", body.Models)
	}
}

func TestModels_Details(t *testing.T) {
	mockA := llm.ModelInfo{
		ID:            "m1",
		Name:          "Mock Model 1",
		Provider:      "mock",
		InputPerMTok:  floatPtr(1.5),
		OutputPerMTok: floatPtr(3.0),
		SupportsTools: true,
		WeeklyTokens:  1000,
	}
	mockB := llm.ModelInfo{
		ID:            "n1",
		Name:          "Other Model",
		Provider:      "other",
		InputPerMTok:  floatPtr(2.0),
		OutputPerMTok: floatPtr(4.0),
	}
	var lastFilter string
	h := NewHarness(t, &HarnessOpts{
		ModelDetailLister: func(_ context.Context, providerFilter string) []llm.ModelInfo {
			lastFilter = providerFilter
			if providerFilter == "" {
				return []llm.ModelInfo{mockA, mockB}
			}
			out := []llm.ModelInfo{}
			for _, m := range []llm.ModelInfo{mockA, mockB} {
				if m.Provider == providerFilter {
					out = append(out, m)
				}
			}
			return out
		},
	})

	// No filter: both models.
	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/models/details", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Models []llm.ModelInfo `json:"models"`
	}
	DecodeJSON(t, rec, &body)
	if len(body.Models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(body.Models))
	}
	first := body.Models[0]
	if first.ID != "m1" || first.Provider != "mock" {
		t.Errorf("first = %+v, want id=m1 provider=mock", first)
	}
	if first.InputPerMTok == nil || *first.InputPerMTok != 1.5 {
		t.Errorf("first.InputPerMTok = %v, want 1.5", first.InputPerMTok)
	}
	if first.OutputPerMTok == nil || *first.OutputPerMTok != 3.0 {
		t.Errorf("first.OutputPerMTok = %v, want 3.0", first.OutputPerMTok)
	}
	if !first.SupportsTools {
		t.Errorf("first.SupportsTools = false, want true")
	}

	// With ?provider=mock: only mock entry.
	rec = h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/models/details?provider=mock", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body.Models = nil
	DecodeJSON(t, rec, &body)
	if len(body.Models) != 1 || body.Models[0].ID != "m1" {
		t.Errorf("filtered models = %+v, want only m1", body.Models)
	}
	if lastFilter != "mock" {
		t.Errorf("lastFilter = %q, want mock", lastFilter)
	}
}

func TestProviders_List(t *testing.T) {
	h := providerCrudHarness(t)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/llm/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	DecodeJSON(t, rec, &body)

	if body["default_provider"] != "mock-existing" {
		t.Errorf("default_provider = %v, want mock-existing", body["default_provider"])
	}

	providers, ok := body["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("providers = %v, want one entry", body["providers"])
	}
	first, _ := providers[0].(map[string]any)
	if first["name"] != "mock-existing" {
		t.Errorf("provider name = %v, want mock-existing", first["name"])
	}
	if first["type"] != "openai" {
		t.Errorf("provider type = %v, want openai", first["type"])
	}
	if first["api_key_set"] != true {
		t.Errorf("api_key_set = %v, want true", first["api_key_set"])
	}
	// Redaction: the actual API key must never appear in the response.
	if _, present := first["api_key"]; present {
		t.Errorf("api_key field leaked in response: %v", first["api_key"])
	}
}

func floatPtr(f float64) *float64 { return &f }
