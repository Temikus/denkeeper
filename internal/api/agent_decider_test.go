package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

type stubDecisionProvider struct{}

func (stubDecisionProvider) Decide(context.Context, llm.DecisionRequest) (*llm.DecisionResponse, error) {
	return &llm.DecisionResponse{}, nil
}

// deciderDeps is testDeps with a supervised "default" agent, a running decider
// "jev", and a decider "late" that is configured but not in the live set.
func deciderDeps() Deps {
	deps := testDeps()
	deps.Config = config.NewHolder(&config.Config{
		Session: config.SessionConfig{Tier: "supervised"},
		LLM: config.LLMConfig{Deciders: []config.DeciderConfig{
			{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13"},
			{Name: "late", Provider: "or", Model: "typesafe/jev-1.13"},
		}},
		Agents: []config.AgentInstanceConfig{{Name: "default", Adapters: []string{"telegram"}}},
	})
	deps.Deciders = llm.NewDeciderSet(nil, deps.CostTracker)
	deps.Deciders.Put(stubDecider("jev", deps.CostTracker))
	return deps
}

func stubDecider(name string, costs *llm.CostTracker) *llm.Decider {
	return llm.NewDecider(llm.DeciderConfig{Name: name, Provider: "or", Model: "typesafe/jev-1.13", Timeout: time.Second}, stubDecisionProvider{}, costs)
}

func patchAgent(t *testing.T, srv *Server, fields map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(fields)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/default", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

// patchError returns the decoded "error" field of a PATCH response.
func patchError(rec *httptest.ResponseRecorder) string {
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body["error"]
}

func mustPatchAgent(t *testing.T, srv *Server, fields map[string]any) {
	t.Helper()
	if rec := patchAgent(t, srv, fields); rec.Code != http.StatusOK {
		t.Fatalf("PATCH %v: status = %d; body: %s", fields, rec.Code, rec.Body.String())
	}
}

// assertPatchRejected checks a 400 naming wantErr and that no decider got wired.
func assertPatchRejected(t *testing.T, deps Deps, fields map[string]any, wantErr string) {
	t.Helper()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	rec := patchAgent(t, srv, fields)
	if msg := patchError(rec); rec.Code != http.StatusBadRequest || !strings.Contains(msg, wantErr) {
		t.Fatalf("status = %d, error = %q; want 400 containing %q", rec.Code, msg, wantErr)
	}
	if deps.Dispatcher.Agent("default").SupervisorDecider() != nil {
		t.Error("a rejected request wired a decider")
	}
	if got := deps.Config.Get().Agents[0].SupervisorDecider; got != "" {
		t.Errorf("a rejected request stored supervisor_decider = %q", got)
	}
}

func TestAgentConfigUpdate_BindsDeciderInEnforceMode(t *testing.T) {
	deps := deciderDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": "jev", "supervisor_decider_mode": "enforce"})

	e := deps.Dispatcher.Agent("default")
	if d := e.SupervisorDecider(); d == nil || d.Name() != "jev" {
		t.Fatalf("wired decider = %v, want jev", d)
	}
	want := agent.DeciderStageConfig{Mode: "enforce", ApproveAt: 0.95, DenyAt: 0.05}
	if got, _ := e.SupervisorDeciderConfig(); got != want {
		t.Errorf("live stage = %+v, want %+v (omitted thresholds take the defaults)", got, want)
	}

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents/default"))
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["supervisor_decider"] != "jev" || resp["supervisor_decider_mode"] != "enforce" ||
		resp["supervisor_decider_approve_at"] != 0.95 || resp["supervisor_decider_deny_at"] != 0.05 {
		t.Errorf("GET decider fields = %v/%v/%v/%v", resp["supervisor_decider"], resp["supervisor_decider_mode"],
			resp["supervisor_decider_approve_at"], resp["supervisor_decider_deny_at"])
	}
}

func TestAgentConfigUpdate_RetunesWiredDecider(t *testing.T) {
	deps := deciderDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": "jev", "supervisor_decider_mode": "enforce"})

	mustPatchAgent(t, srv, map[string]any{"supervisor_decider_approve_at": 0.9, "supervisor_decider_deny_at": 0.2})

	want := agent.DeciderStageConfig{Mode: "enforce", ApproveAt: 0.9, DenyAt: 0.2}
	if got, _ := deps.Dispatcher.Agent("default").SupervisorDeciderConfig(); got != want {
		t.Errorf("live stage = %+v, want %+v (mode must survive a thresholds-only patch)", got, want)
	}
}

func TestAgentConfigUpdate_ZeroThresholdRestoresDefault(t *testing.T) {
	deps := deciderDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": "jev", "supervisor_decider_approve_at": 0.8})

	mustPatchAgent(t, srv, map[string]any{"supervisor_decider_approve_at": 0})

	if got, _ := deps.Dispatcher.Agent("default").SupervisorDeciderConfig(); got.ApproveAt != 0.95 {
		t.Errorf("live approve_at = %v, want the 0.95 default", got.ApproveAt)
	}
	if got := deps.Config.Get().Agents[0].SupervisorDeciderApproveAt; got != 0.95 {
		t.Errorf("stored approve_at = %v, want 0.95", got)
	}
}

func TestAgentConfigUpdate_EmptyDeciderUnbinds(t *testing.T) {
	deps := deciderDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": "jev"})

	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": ""})

	if deps.Dispatcher.Agent("default").SupervisorDecider() != nil {
		t.Error("decider still wired after clearing")
	}
	if got := deps.Config.Get().Agents[0].SupervisorDecider; got != "" {
		t.Errorf("stored supervisor_decider = %q, want empty", got)
	}
}

func TestAgentConfigUpdate_RebindAfterClearStartsFromDefaults(t *testing.T) {
	deps := deciderDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": "jev", "supervisor_decider_mode": "enforce",
		"supervisor_decider_approve_at": 0.6, "supervisor_decider_deny_at": 0.4})
	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": ""})

	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": "jev"})

	want := agent.DeciderStageConfig{Mode: "shadow", ApproveAt: 0.95, DenyAt: 0.05}
	if got, _ := deps.Dispatcher.Agent("default").SupervisorDeciderConfig(); got != want {
		t.Errorf("live stage = %+v, want %+v (a cleared decider's enforce tuning must not come back)", got, want)
	}
	if ac := deps.Config.Get().Agents[0]; ac.SupervisorDeciderMode != "shadow" || ac.SupervisorDeciderApproveAt != 0.95 {
		t.Errorf("stored mode/approve_at = %q/%v, want shadow/0.95", ac.SupervisorDeciderMode, ac.SupervisorDeciderApproveAt)
	}
}

func TestAgentConfigUpdate_DeciderThresholdsOutOfOrderRejected(t *testing.T) {
	assertPatchRejected(t, deciderDeps(), map[string]any{
		"supervisor_decider": "jev", "supervisor_decider_approve_at": 0.3, "supervisor_decider_deny_at": 0.5,
	}, "0 < deny_at < approve_at < 1")
}

func TestAgentConfigUpdate_UnknownDeciderRejected(t *testing.T) {
	assertPatchRejected(t, deciderDeps(), map[string]any{"supervisor_decider": "nope"}, "does not match any [[llm.deciders]]")
}

func TestAgentConfigUpdate_UnknownDeciderModeRejected(t *testing.T) {
	assertPatchRejected(t, deciderDeps(), map[string]any{
		"supervisor_decider": "jev", "supervisor_decider_mode": "audit",
	}, `must be "shadow" or "enforce"`)
}

func TestAgentConfigUpdate_DeciderOnNonSupervisedTierRejected(t *testing.T) {
	assertPatchRejected(t, deciderDeps(), map[string]any{
		"supervisor_decider": "jev", "session_tier": "autonomous",
	}, "only meaningful when the session tier")
}

// A decider client is built at startup, so one that only exists in the config
// cannot be wired live.
func TestAgentConfigUpdate_UnwiredDeciderRejected(t *testing.T) {
	assertPatchRejected(t, deciderDeps(), map[string]any{"supervisor_decider": "late"}, `decider "late" is not running`)
}

// A decider that joins the live set after the server started binds without a
// restart.
func TestAgentConfigUpdate_DeciderAddedAfterStartupBinds(t *testing.T) {
	deps := deciderDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	deps.Deciders.Put(stubDecider("late", deps.CostTracker))

	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": "late"})

	if d := deps.Dispatcher.Agent("default").SupervisorDecider(); d == nil || d.Name() != "late" {
		t.Fatalf("wired decider = %v, want late", d)
	}
}

// Leaving the supervised tier with a decider set would persist a config that
// fails validation on the next load.
func TestAgentConfigUpdate_TierChangeWithDeciderSetRejected(t *testing.T) {
	deps := deciderDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	mustPatchAgent(t, srv, map[string]any{"supervisor_decider": "jev"})

	rec := patchAgent(t, srv, map[string]any{"session_tier": "autonomous"})

	if msg := patchError(rec); rec.Code != http.StatusBadRequest || !strings.Contains(msg, "only meaningful when the session tier") {
		t.Fatalf("status = %d, error = %q; want 400", rec.Code, msg)
	}
	if tier := deps.Dispatcher.Agent("default").PermissionTier(); tier != "supervised" {
		t.Errorf("tier = %q, want supervised: a rejected request must not change the engine", tier)
	}
}

func TestAgentConfigUpdate_RejectedDeciderDoesNotRename(t *testing.T) {
	deps := deciderDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := patchAgent(t, srv, map[string]any{"name": "renamed", "supervisor_decider": "nope"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if deps.Dispatcher.Agent("default") == nil || deps.Dispatcher.Agent("renamed") != nil {
		t.Error("the agent was renamed by a request that was rejected")
	}
	if got := deps.Config.Get().Agents[0].Name; got != "default" {
		t.Errorf("stored name = %q, want default", got)
	}
}

func TestLLMProviders_ListsDeciders(t *testing.T) {
	srv := New(testConfig(allScopesKey()), deciderDeps(), testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/llm/providers"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", rec.Code, rec.Body.String())
	}
	var resp llmProvidersResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := deciderInfo{Name: "jev", Provider: "or", Model: "typesafe/jev-1.13", UsedBy: []string{}}
	if len(resp.Deciders) != 2 || !reflect.DeepEqual(resp.Deciders[0], want) {
		t.Errorf("deciders = %+v, want [%+v, late]", resp.Deciders, want)
	}
}
