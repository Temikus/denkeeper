package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/Temikus/denkeeper/internal/llm"
)

// noEvalScopeKey carries neither eval scope, so it cannot even estimate.
func noEvalScopeKey() config.APIKeyConfig {
	return config.APIKeyConfig{Name: "chat-only", Key: "dk-chat-only", Scopes: []string{"chat"}}
}

// seedConvStats writes n exchanges worth of telemetry against convID. Each
// call to UpdateConversationStats adds one message, so 2n calls make n
// exchanges — the same shape a real conversation accumulates.
func seedConvStats(t *testing.T, srv *Server, convID string, messages int, costEach float64) {
	t.Helper()
	store, ok := srv.deps.Memory.(agent.TelemetryStore)
	if !ok {
		t.Fatal("test memory store is not a TelemetryStore")
	}
	if err := srv.deps.Memory.GetOrCreateConversationByID(context.Background(), convID, "api", "test"); err != nil {
		t.Fatalf("GetOrCreateConversationByID: %v", err)
	}
	for i := 0; i < messages; i++ {
		if err := store.UpdateConversationStats(context.Background(), convID, "default",
			agent.StoredMessage{Cost: costEach}, 0, 0); err != nil {
			t.Fatalf("UpdateConversationStats: %v", err)
		}
	}
}

// pricedModels wires a ModelDetailLister advertising one priced model.
func pricedModels(srv *Server, id string, in, out float64) {
	srv.deps.ModelDetailLister = func(_ context.Context, _ string) []llm.ModelInfo {
		return []llm.ModelInfo{{ID: id, Name: id, Provider: "mock", InputPerMTok: &in, OutputPerMTok: &out}}
	}
}

func decodeEstimate(t *testing.T, body []byte) eval.Estimate {
	t.Helper()
	var got eval.Estimate
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding estimate: %v (body %s)", err, body)
	}
	return got
}

// estimateBody is the standard request against a set named "set".
const estimateBody = `{"task_set":"set","base_agent":"default","variants":[{"name":"current"}],"k":1}`

// --- Scope gating ---

func TestEvalEstimate_ReadScopeIsEnough(t *testing.T) {
	srv, store := evalTestServer(t, allScopesKey(), evalReadOnlyKey())
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate", estimateBody, "dk-eval-readonly")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an estimate spends nothing", rec.Code)
	}
}

func TestEvalEstimate_WithoutAnEvalScopeIsForbidden(t *testing.T) {
	srv, store := evalTestServer(t, allScopesKey(), noEvalScopeKey())
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate", estimateBody, "dk-chat-only")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestEvalConfig_ReadScopeIsEnough(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey(), evalReadOnlyKey())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/config", "", "dk-eval-readonly")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestEvalConfig_WithoutAnEvalScopeIsForbidden(t *testing.T) {
	srv, _ := evalTestServer(t, allScopesKey(), noEvalScopeKey())

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/config", "", "dk-chat-only")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// --- Request validation ---

func TestEvalEstimate_UnknownTaskSetIs404(t *testing.T) {
	srv, _ := evalTestServer(t)

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate",
		`{"task_set":"nope","base_agent":"default","variants":[{"name":"current"}]}`, "dk-test-key")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalEstimate_UnknownAgentIs400(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate",
		`{"task_set":"set","base_agent":"ghost","variants":[{"name":"current"}]}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEvalEstimate_NoVariantsIs400(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate",
		`{"task_set":"set","base_agent":"default","variants":[]}`, "dk-test-key")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEvalEstimate_UnknownModelIsPricedUnknownNotRejected(t *testing.T) {
	// Unlike run creation, an estimate does not validate the overlay: it costs
	// nothing, and an unknown name already shows up as an unknown basis.
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate",
		`{"task_set":"set","base_agent":"default","variants":[{"name":"c","llm_model":"not-a-real-model"}],"k":1}`,
		"dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEstimate(t, rec.Body.Bytes())
	if got.Basis != eval.BasisUnknown {
		t.Errorf("basis = %q, want %q", got.Basis, eval.BasisUnknown)
	}
}

// --- Bases end to end ---

func TestEvalEstimate_HistoryBasisFromTheSourceConversation(t *testing.T) {
	srv, store := evalTestServer(t)
	seedConvStats(t, srv, "tg:1", 4, 0.25) // $1.00 over 4 messages = $0.50 a turn

	set, err := store.CreateTaskSet(context.Background(), "set", "")
	if err != nil {
		t.Fatalf("CreateTaskSet: %v", err)
	}
	if _, err := store.AddTask(context.Background(), set.ID, eval.Task{
		Prompt: "hi", Category: eval.CategoryChat, SourceConversationID: "tg:1",
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate", estimateBody, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeEstimate(t, rec.Body.Bytes())
	if got.Basis != eval.BasisHistory {
		t.Fatalf("basis = %q, want %q", got.Basis, eval.BasisHistory)
	}
	if got.Currency != "USD" || got.Tasks != 1 || got.K != 1 {
		t.Errorf("envelope = %+v, want currency USD, tasks 1, k 1", got)
	}
	if got.Low <= 0 || got.High <= got.Low {
		t.Errorf("range = %v..%v, want a real widening range", got.Low, got.High)
	}
	if len(got.PerVariant) != 1 || got.PerVariant[0].Name != "current" {
		t.Errorf("per_variant = %+v, want one entry named current", got.PerVariant)
	}
}

func TestEvalEstimate_ListPriceBasisFromModelDetails(t *testing.T) {
	srv, store := evalTestServer(t)
	// The test engine's default model; no task carries a source conversation.
	pricedModels(srv, "test-model", 3, 15)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate", estimateBody, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	got := decodeEstimate(t, rec.Body.Bytes())
	if got.Basis != eval.BasisListPrice {
		t.Fatalf("basis = %q, want %q", got.Basis, eval.BasisListPrice)
	}
	if got.Low <= 0 || got.High <= got.Low {
		t.Errorf("range = %v..%v, want prompt-only below prompt+output", got.Low, got.High)
	}
}

func TestEvalEstimate_UnknownBasisWithoutHistoryOrPricing(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate", estimateBody, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEstimate(t, rec.Body.Bytes())
	if got.Basis != eval.BasisUnknown {
		t.Fatalf("basis = %q, want %q", got.Basis, eval.BasisUnknown)
	}
	if got.Low != 0 || got.High != 0 {
		t.Errorf("range = %v..%v, want 0..0 — the caller shows the cap alone", got.Low, got.High)
	}
}

func TestEvalEstimate_OmittedKTakesTheConfiguredDefault(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "hi")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate",
		`{"task_set":"set","base_agent":"default","variants":[{"name":"current"}]}`, "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := decodeEstimate(t, rec.Body.Bytes()); got.K != 3 {
		t.Errorf("k = %d, want the runner's default_k of 3", got.K)
	}
}

func TestEvalEstimate_SampleTasksNarrowsTheReportedTaskCount(t *testing.T) {
	srv, store := evalTestServer(t)
	seedTaskSet(t, store, "set", "a", "b", "c", "d")

	rec := evalRequest(t, srv, http.MethodPost, "/api/v1/eval/estimate",
		`{"task_set":"set","base_agent":"default","variants":[{"name":"current"}],"k":1,"sample_tasks":2}`,
		"dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decodeEstimate(t, rec.Body.Bytes())
	if got.Tasks != 2 {
		t.Errorf("tasks = %d, want 2 — the drawn subset, not the set size", got.Tasks)
	}
	if got.Note == "" {
		t.Error("note is empty; the subset is drawn at run time and the figure is a mean")
	}
}

// --- Config endpoint ---

func TestEvalConfig_ReflectsTOMLValues(t *testing.T) {
	srv, _ := evalTestServer(t)
	srv.deps.Config.Get().Eval = config.EvalConfig{
		Audit:              "summary",
		MaxConcurrent:      4,
		MaxCostPerRun:      7.5,
		DefaultK:           5,
		CompletenessFloor:  0.9,
		WinThreshold:       0.6,
		GateRejectedRatePP: 1.5,
		GateRoundsPct:      10,
		GateCostPct:        30,
	}

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/config", "", "dk-test-key")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got evalConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding config: %v", err)
	}
	want := evalConfigResponse{
		DefaultK: 5, MaxCostPerRun: 7.5, MaxConcurrent: 4,
		CompletenessFloor: 0.9, WinThreshold: 0.6,
		GateRejectedRatePP: 1.5, GateRoundsPct: 10, GateCostPct: 30,
		Audit: "summary",
	}
	if got != want {
		t.Errorf("config = %+v, want %+v", got, want)
	}
}

func TestEvalConfig_AuditDefaultsToFull(t *testing.T) {
	srv, _ := evalTestServer(t)
	srv.deps.Config.Get().Eval = config.EvalConfig{DefaultK: 3}

	rec := evalRequest(t, srv, http.MethodGet, "/api/v1/eval/config", "", "dk-test-key")
	var got evalConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding config: %v", err)
	}
	if got.Audit != "full" {
		t.Errorf("audit = %q, want full", got.Audit)
	}
}
