package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
)

// deciderReviewDeps is deciderDeps with an audit store holding one shadow
// review of "default"'s echo call and Argus's approval of it, at the given time.
func deciderReviewDeps(t *testing.T, at time.Time) Deps {
	t.Helper()
	at = at.UTC() // as the audit emitter stamps them
	deps := deciderDeps()
	store, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	insert := func(ev audit.Event) {
		t.Helper()
		if err := store.Insert(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	insert(audit.Event{
		Timestamp: at, Category: audit.CategorySupervisor, Action: "review", Agent: "default",
		Status: audit.StatusOK, Source: "decider:jev", ConversationID: "c1",
		Detail: `{"tool":"echo","arguments":"{}","decision":"shadow","model":"typesafe/jev-1.13","cost":0.0001,` +
			`"answers":{"aligned":{"type":"noul","noul":0.99},"safe_args":{"type":"noul","noul":0.97},"scoped":{"type":"noul","noul":0.98}}}`,
	})
	insert(audit.Event{
		Timestamp: at.Add(time.Second), Category: audit.CategorySupervisor, Action: "review", Agent: "default",
		Status: audit.StatusOK, Source: "supervisor:argus", ConversationID: "c1",
		Detail: `{"tool":"echo","arguments":"{}","decision":"APPROVE","supervisor":"argus","cost":0.04}`,
	})
	deps.AuditStore = store
	return deps
}

func getDeciderReviews(t *testing.T, srv *Server, query string) (*httptest.ResponseRecorder, deciderReviewsResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents/default/decider-reviews"+query))
	var body deciderReviewsResponse
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec, body
}

func TestDeciderReviews_PairsStoredReviews(t *testing.T) {
	srv := New(testConfig(allScopesKey()), deciderReviewDeps(t, time.Now().Add(-time.Hour)), testLogger())

	rec, body := getDeciderReviews(t, srv, "?decider=jev")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body.Decider != "jev" || len(body.Reviews) != 1 {
		t.Fatalf("body = %+v, want one review by jev", body)
	}
	r := body.Reviews[0]
	if r.Supervisor != "APPROVE" || r.MinScore == nil || *r.MinScore != 0.97 || r.Lowest != "safe_args" {
		t.Errorf("review = %+v, want APPROVE with min 0.97 on safe_args", r)
	}
	if r.SupervisorCost == nil || *r.SupervisorCost != 0.04 {
		t.Errorf("supervisor cost = %v, want 0.04", r.SupervisorCost)
	}
}

func TestDeciderReviews_DefaultsToTheWiredDecider(t *testing.T) {
	deps := deciderReviewDeps(t, time.Now().Add(-time.Hour))
	deps.Dispatcher.Agent("default").SetSupervisorDecider(stubDecider("jev", deps.CostTracker), agent.DeciderStageConfig{Mode: "shadow"})
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec, body := getDeciderReviews(t, srv, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body.Decider != "jev" || len(body.Reviews) != 1 {
		t.Errorf("body = %+v, want jev's one review", body)
	}
}

func TestDeciderReviews_NoDeciderIs400(t *testing.T) {
	srv := New(testConfig(allScopesKey()), deciderReviewDeps(t, time.Now()), testLogger())
	if rec, _ := getDeciderReviews(t, srv, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when neither the query nor the agent names a decider", rec.Code)
	}
}

func TestDeciderReviews_SinceDropsOlderReviews(t *testing.T) {
	srv := New(testConfig(allScopesKey()), deciderReviewDeps(t, time.Now().Add(-48*time.Hour)), testLogger())
	since := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)

	rec, body := getDeciderReviews(t, srv, "?decider=jev&since="+since)
	if rec.Code != http.StatusOK || len(body.Reviews) != 0 {
		t.Errorf("status %d with %d reviews, want 200 with none", rec.Code, len(body.Reviews))
	}
}

func TestDeciderReviews_BadSinceIs400(t *testing.T) {
	srv := New(testConfig(allScopesKey()), deciderReviewDeps(t, time.Now()), testLogger())
	if rec, _ := getDeciderReviews(t, srv, "?decider=jev&since=yesterday"); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDeciderReviews_UnknownAgentIs404(t *testing.T) {
	srv := New(testConfig(allScopesKey()), deciderReviewDeps(t, time.Now()), testLogger())
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents/nobody/decider-reviews?decider=jev"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestDeciderReviews_NoAuditStoreIs503(t *testing.T) {
	srv := New(testConfig(allScopesKey()), deciderDeps(), testLogger())
	if rec, _ := getDeciderReviews(t, srv, "?decider=jev"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// The reviews carry tool arguments from the audit log, so agents:read alone
// is not enough.
func TestDeciderReviews_NeedsAuditScope(t *testing.T) {
	key := config.APIKeyConfig{Name: "test", Key: "dk-test-key", Scopes: []string{"agents:read", "admin"}}
	srv := New(testConfig(key), deciderReviewDeps(t, time.Now()), testLogger())
	if rec, _ := getDeciderReviews(t, srv, "?decider=jev"); rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
