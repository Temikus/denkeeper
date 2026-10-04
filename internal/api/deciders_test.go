package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// fakeDeciderRuntime records the snapshots pushed to the live deciders and
// answers tests with resp or err.
type fakeDeciderRuntime struct {
	mu      sync.Mutex
	applied []*config.Config
	tested  []config.DeciderConfig
	resp    *llm.DecisionResponse
	err     error
}

func (f *fakeDeciderRuntime) Apply(snap *config.Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, snap)
}

func (f *fakeDeciderRuntime) Test(_ context.Context, dc config.DeciderConfig) (*llm.DecisionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = append(f.tested, dc)
	return f.resp, f.err
}

func (f *fakeDeciderRuntime) lastApplied() *config.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.applied) == 0 {
		return nil
	}
	return f.applied[len(f.applied)-1]
}

const deciderTOML = `
[telegram]
token = "test"
allowed_users = [1]

[llm]
default_provider = "anthropic"

[[llm.providers]]
name = "anthropic"
type = "anthropic"
api_key = "sk-test"

[[llm.providers]]
name = "or"
type = "openrouter"
api_key = "sk-or-test"
`

const deciderInUseTOML = deciderTOML + `
[[llm.deciders]]
name = "jev"
provider = "or"
model = "typesafe/jev-1.13"

[[agents]]
name = "pamela"
session_tier = "supervised"
supervisor_decider = "jev"
`

// deciderTestServer returns a server whose in-memory config and TOML file are
// both toml, with rt as the decider runtime.
func deciderTestServer(t *testing.T, toml string, rt *fakeDeciderRuntime) (*Server, string) {
	t.Helper()
	cfg, err := config.Parse([]byte(toml))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	s, path := providerTestServer(t, cfg, toml, nil)
	if rt != nil {
		s.deps.DeciderRuntime = rt
	}
	return s, path
}

func deciderRequest(method, target, name, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if name != "" {
		r.SetPathValue("name", name)
	}
	return r
}

func mustLoad(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("written config does not load: %v", err)
	}
	return cfg
}

func TestCreateDecider_PersistsAndAppliesLive(t *testing.T) {
	rt := &fakeDeciderRuntime{}
	s, path := deciderTestServer(t, deciderTOML, rt)

	rec := httptest.NewRecorder()
	s.handleCreateDecider(rec, deciderRequest(http.MethodPost, "/api/v1/llm/deciders", "", `{"name":"jev","provider":"or","model":"typesafe/jev-1.13"}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var resp deciderMutationResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.RestartRequired || resp.Decider.Timeout != "5s" || resp.Decider.MaxInputTokens != 30000 {
		t.Errorf("response = %+v, want live with defaults filled", resp)
	}
	if snap := rt.lastApplied(); snap == nil || len(snap.LLM.Deciders) != 1 || snap.LLM.Deciders[0].Name != "jev" {
		t.Errorf("runtime not given the new decider: %+v", snap)
	}
	if got := mustLoad(t, path).LLM.Deciders; len(got) != 1 || got[0].Model != "typesafe/jev-1.13" {
		t.Errorf("persisted deciders = %+v", got)
	}
}

func TestCreateDecider_Duplicate409(t *testing.T) {
	s, _ := deciderTestServer(t, deciderInUseTOML, &fakeDeciderRuntime{})

	rec := httptest.NewRecorder()
	s.handleCreateDecider(rec, deciderRequest(http.MethodPost, "/api/v1/llm/deciders", "", `{"name":"jev","provider":"or","model":"m"}`))

	if rec.Code != http.StatusConflict {
		t.Errorf("status %d, want 409", rec.Code)
	}
}

func TestCreateDecider_NonDecisionProvider400(t *testing.T) {
	s, path := deciderTestServer(t, deciderTOML, &fakeDeciderRuntime{})

	rec := httptest.NewRecorder()
	s.handleCreateDecider(rec, deciderRequest(http.MethodPost, "/api/v1/llm/deciders", "", `{"name":"jev","provider":"anthropic","model":"m"}`))

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not serve decision models") {
		t.Errorf("status %d, body %s; want 400 naming the provider type", rec.Code, rec.Body)
	}
	if got := mustLoad(t, path).LLM.Deciders; len(got) != 0 {
		t.Errorf("rejected decider was written: %+v", got)
	}
}

func TestCreateDecider_NoRuntimeRestartRequired(t *testing.T) {
	s, _ := deciderTestServer(t, deciderTOML, nil)

	rec := httptest.NewRecorder()
	s.handleCreateDecider(rec, deciderRequest(http.MethodPost, "/api/v1/llm/deciders", "", `{"name":"jev","provider":"or","model":"m"}`))

	var resp deciderMutationResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if rec.Code != http.StatusCreated || !resp.RestartRequired {
		t.Errorf("status %d, restart_required %v; want 201 and true", rec.Code, resp.RestartRequired)
	}
}

func TestPatchDecider_ChangesModelAndRestoresDefault(t *testing.T) {
	rt := &fakeDeciderRuntime{}
	s, path := deciderTestServer(t, deciderInUseTOML, rt)

	rec := httptest.NewRecorder()
	s.handlePatchDecider(rec, deciderRequest(http.MethodPatch, "/api/v1/llm/deciders/jev", "jev", `{"model":"typesafe/jev-2","timeout":""}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	got := mustLoad(t, path).LLM.Deciders
	if len(got) != 1 || got[0].Model != "typesafe/jev-2" || got[0].Timeout != config.DefaultDeciderTimeout {
		t.Errorf("persisted = %+v, want jev-2 with the default timeout", got)
	}
	if snap := rt.lastApplied(); snap == nil || snap.LLM.Deciders[0].Model != "typesafe/jev-2" {
		t.Error("runtime not given the changed decider")
	}
}

func TestPatchDecider_Unknown404(t *testing.T) {
	s, _ := deciderTestServer(t, deciderTOML, &fakeDeciderRuntime{})

	rec := httptest.NewRecorder()
	s.handlePatchDecider(rec, deciderRequest(http.MethodPatch, "/api/v1/llm/deciders/nope", "nope", `{"model":"m"}`))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestPatchDecider_Rename400(t *testing.T) {
	s, _ := deciderTestServer(t, deciderInUseTOML, &fakeDeciderRuntime{})

	rec := httptest.NewRecorder()
	s.handlePatchDecider(rec, deciderRequest(http.MethodPatch, "/api/v1/llm/deciders/jev", "jev", `{"name":"jev2"}`))

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "cannot be renamed") {
		t.Errorf("status %d, body %s; want 400 refusing the rename", rec.Code, rec.Body)
	}
}

func TestDeleteDecider_InUse409ListsUsers(t *testing.T) {
	s, path := deciderTestServer(t, deciderInUseTOML, &fakeDeciderRuntime{})

	rec := httptest.NewRecorder()
	s.handleDeleteDecider(rec, deciderRequest(http.MethodDelete, "/api/v1/llm/deciders/jev", "jev", ""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", rec.Code)
	}
	var body struct {
		UsedBy []string `json:"used_by"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if len(body.UsedBy) != 1 || body.UsedBy[0] != "agent:pamela" {
		t.Errorf("used_by = %v, want [agent:pamela]", body.UsedBy)
	}
	if got := mustLoad(t, path).LLM.Deciders; len(got) != 1 {
		t.Error("an in-use decider was removed from the file")
	}
}

func TestDeleteDecider_RemovesAndAppliesLive(t *testing.T) {
	rt := &fakeDeciderRuntime{}
	s, path := deciderTestServer(t, deciderTOML+`
[[llm.deciders]]
name = "jev"
provider = "or"
model = "typesafe/jev-1.13"
`, rt)

	rec := httptest.NewRecorder()
	s.handleDeleteDecider(rec, deciderRequest(http.MethodDelete, "/api/v1/llm/deciders/jev", "jev", ""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if got := mustLoad(t, path).LLM.Deciders; len(got) != 0 {
		t.Errorf("persisted deciders = %+v, want none", got)
	}
	if snap := rt.lastApplied(); snap == nil || len(snap.LLM.Deciders) != 0 {
		t.Error("runtime still holds the deleted decider")
	}
}

func TestTestDecider_UnsavedProviderAndModel(t *testing.T) {
	rt := &fakeDeciderRuntime{resp: &llm.DecisionResponse{Model: "typesafe/jev-1.13", CostUSD: 0.00004}}
	s, _ := deciderTestServer(t, deciderTOML, rt)

	rec := httptest.NewRecorder()
	s.handleTestDecider(rec, deciderRequest(http.MethodPost, "/api/v1/llm/deciders/test", "", `{"provider":"or","model":"typesafe/jev-1.13"}`))

	var resp deciderTestResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if rec.Code != http.StatusOK || resp.Status != "ok" || resp.CostUSD != 0.00004 {
		t.Errorf("status %d, resp %+v; want ok with the cost", rec.Code, resp)
	}
	if len(rt.tested) != 1 || rt.tested[0].Timeout != "5s" {
		t.Errorf("tested = %+v, want the unsaved decider with defaults", rt.tested)
	}
}

func TestTestDecider_ProviderErrorIs200(t *testing.T) {
	rt := &fakeDeciderRuntime{err: errors.New("upstream said no")}
	s, _ := deciderTestServer(t, deciderInUseTOML, rt)

	rec := httptest.NewRecorder()
	s.handleTestDecider(rec, deciderRequest(http.MethodPost, "/api/v1/llm/deciders/test", "", `{"name":"jev"}`))

	var resp deciderTestResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if rec.Code != http.StatusOK || resp.Status != "error" || !strings.Contains(resp.Message, "upstream said no") {
		t.Errorf("status %d, resp %+v; want 200 with status error", rec.Code, resp)
	}
}

func TestTestDecider_UnknownName404(t *testing.T) {
	s, _ := deciderTestServer(t, deciderTOML, &fakeDeciderRuntime{})

	rec := httptest.NewRecorder()
	s.handleTestDecider(rec, deciderRequest(http.MethodPost, "/api/v1/llm/deciders/test", "", `{"name":"nope"}`))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestLLMProviders_MarksDecisionProvidersAndUsers(t *testing.T) {
	s, _ := deciderTestServer(t, deciderInUseTOML, nil)

	rec := httptest.NewRecorder()
	s.handleGetLLMProviders(rec, httptest.NewRequest(http.MethodGet, "/api/v1/llm/providers", nil))

	var resp llmProvidersResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	serves := map[string]bool{}
	for _, p := range resp.Providers {
		serves[p.Name] = p.ServesDecisions
	}
	if !serves["or"] || serves["anthropic"] {
		t.Errorf("serves_decisions = %v, want only or", serves)
	}
	if len(resp.Deciders) != 1 || len(resp.Deciders[0].UsedBy) != 1 || resp.Deciders[0].UsedBy[0] != "agent:pamela" {
		t.Errorf("deciders = %+v, want jev used by agent:pamela", resp.Deciders)
	}
}
