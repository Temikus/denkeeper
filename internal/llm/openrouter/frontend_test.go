package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// frontendFindResponse mirrors the shape fetchFrontendData decodes.
type frontendFindResponse struct {
	Data struct {
		Models    []frontendModel           `json:"models"`
		Analytics map[string]analyticsEntry `json:"analytics"`
	} `json:"data"`
}

// newFrontendStub serves a /models/find payload and records the paths it saw.
func newFrontendStub(t *testing.T, status int, body *frontendFindResponse) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if status != http.StatusOK {
			w.WriteHeader(status)
			// OpenRouter serves an HTML app shell on a missing API path.
			_, _ = w.Write([]byte("<!DOCTYPE html><html></html>"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	return server, &paths
}

// TestFrontendBaseURL_IsVersioned pins the versioned namespace. OpenRouter
// moved /api/frontend → /api/frontend/v1 without announcement, which silently
// zeroed every model's weekly_tokens and flattened popularity sorting.
func TestFrontendBaseURL_IsVersioned(t *testing.T) {
	if !strings.HasPrefix(frontendBaseURL, "https://openrouter.ai/api/frontend/") {
		t.Fatalf("frontendBaseURL = %q, want an openrouter.ai/api/frontend/* URL", frontendBaseURL)
	}
	if !strings.HasSuffix(frontendBaseURL, "/v1") {
		t.Errorf("frontendBaseURL = %q, want the versioned /v1 namespace; the unversioned path 404s", frontendBaseURL)
	}
}

func TestFetchFrontendData_MapsAnalyticsByPermaslug(t *testing.T) {
	var body frontendFindResponse
	body.Data.Models = []frontendModel{
		{Slug: "vendor/model-a", Permaslug: "vendor/model-a-20260101"},
	}
	body.Data.Analytics = map[string]analyticsEntry{
		"vendor/model-a-20260101": {
			ModelPermaslug:    "vendor/model-a-20260101",
			Variant:           "standard",
			TotalPromptTokens: 100, TotalCompletionTokens: 25,
		},
	}
	server, paths := newFrontendStub(t, http.StatusOK, &body)

	c := New("test-key")
	c.frontendURL = server.URL

	permaslugs, analytics := c.fetchFrontendData(context.Background())

	if got := (*paths); len(got) != 1 || got[0] != "/models/find" {
		t.Errorf("requested paths = %v, want [/models/find]", got)
	}
	if got := permaslugs["vendor/model-a"]; got != "vendor/model-a-20260101" {
		t.Errorf("permaslug = %q, want vendor/model-a-20260101", got)
	}
	a, ok := analytics["vendor/model-a-20260101"]
	if !ok {
		t.Fatal("analytics missing entry for permaslug")
	}
	if total := a.TotalPromptTokens + a.TotalCompletionTokens; total != 125 {
		t.Errorf("weekly tokens = %d, want 125", total)
	}
}

// Variant IDs such as "model:free" come back from /api/v1/models but analytics
// is keyed by the base permaslug, so variant forms must be pre-registered.
func TestFetchFrontendData_RegistersVariantSlugs(t *testing.T) {
	var body frontendFindResponse
	body.Data.Models = []frontendModel{
		{Slug: "vendor/model-a", Permaslug: "vendor/model-a-20260101"},
	}
	body.Data.Analytics = map[string]analyticsEntry{
		"vendor/model-a-20260101": {Variant: "free", TotalPromptTokens: 10},
		"vendor/model-b-20260101": {Variant: "standard", TotalPromptTokens: 5},
	}
	server, _ := newFrontendStub(t, http.StatusOK, &body)

	c := New("test-key")
	c.frontendURL = server.URL

	permaslugs, _ := c.fetchFrontendData(context.Background())

	if got := permaslugs["vendor/model-a:free"]; got != "vendor/model-a-20260101" {
		t.Errorf("variant slug mapped to %q, want vendor/model-a-20260101", got)
	}
	// "standard" is the default variant and must not produce a ":standard" key.
	if _, ok := permaslugs["vendor/model-a:standard"]; ok {
		t.Error("standard variant should not be registered as a suffixed slug")
	}
}

// A moved or removed endpoint must degrade gracefully — nil maps, no panic —
// so model listing keeps working without popularity data.
func TestFetchFrontendData_NotFoundReturnsEmpty(t *testing.T) {
	server, _ := newFrontendStub(t, http.StatusNotFound, nil)

	c := New("test-key")
	c.frontendURL = server.URL

	permaslugs, analytics := c.fetchFrontendData(context.Background())

	if len(permaslugs) != 0 || len(analytics) != 0 {
		t.Errorf("got permaslugs=%d analytics=%d, want both empty on 404", len(permaslugs), len(analytics))
	}
}

// The frontend API is best-effort: when it is unreachable, model details must
// still be returned, just without weekly token counts.
func TestListModelDetails_SurvivesFrontendFailure(t *testing.T) {
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/model-a","name":"Model A",
			"pricing":{"prompt":"0.000001","completion":"0.000002"},
			"supported_parameters":["tools"]}]}`))
	}))
	defer models.Close()

	frontend, _ := newFrontendStub(t, http.StatusNotFound, nil)

	c := New("test-key")
	c.baseURL = models.URL
	c.frontendURL = frontend.URL

	got, err := c.ListModelDetails(context.Background())
	if err != nil {
		t.Fatalf("ListModelDetails returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d models, want 1", len(got))
	}
	if got[0].ID != "vendor/model-a" || !got[0].SupportsTools {
		t.Errorf("unexpected model: %+v", got[0])
	}
	if got[0].WeeklyTokens != 0 {
		t.Errorf("WeeklyTokens = %d, want 0 when analytics unavailable", got[0].WeeklyTokens)
	}
}

// End-to-end through ListModelDetails: analytics resolved via the permaslug
// indirection must land on ModelInfo.WeeklyTokens, which drives the sort.
func TestListModelDetails_PopulatesWeeklyTokens(t *testing.T) {
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/model-a","name":"Model A",
			"pricing":{"prompt":"0.000001","completion":"0.000002"},
			"supported_parameters":["tools"]}]}`))
	}))
	defer models.Close()

	var body frontendFindResponse
	body.Data.Models = []frontendModel{
		{Slug: "vendor/model-a", Permaslug: "vendor/model-a-20260101"},
	}
	// Analytics is re-keyed by the model_permaslug *field*, not the JSON map
	// key, and summed across variants — so both variants below aggregate.
	body.Data.Analytics = map[string]analyticsEntry{
		"vendor/model-a-20260101:standard": {
			ModelPermaslug:    "vendor/model-a-20260101",
			Variant:           "standard",
			TotalPromptTokens: 900, TotalCompletionTokens: 50,
		},
		"vendor/model-a-20260101:free": {
			ModelPermaslug:        "vendor/model-a-20260101",
			Variant:               "free",
			TotalCompletionTokens: 50,
		},
	}
	frontend, _ := newFrontendStub(t, http.StatusOK, &body)

	c := New("test-key")
	c.baseURL = models.URL
	c.frontendURL = frontend.URL

	got, err := c.ListModelDetails(context.Background())
	if err != nil {
		t.Fatalf("ListModelDetails returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d models, want 1", len(got))
	}
	if got[0].WeeklyTokens != 1000 {
		t.Errorf("WeeklyTokens = %d, want 1000", got[0].WeeklyTokens)
	}
}
