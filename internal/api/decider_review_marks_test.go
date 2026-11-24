package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
)

// countingStore counts List calls on a real store. onList, when set, runs once
// inside the next List call, to interleave a request with a count.
type countingStore struct {
	*audit.SQLiteStore
	lists  atomic.Int32
	onList func()
}

func (s *countingStore) List(ctx context.Context, opts audit.ListOpts) ([]audit.Event, int, error) {
	s.lists.Add(1)
	if hook := s.onList; hook != nil {
		s.onList = nil
		hook()
	}
	return s.SQLiteStore.List(ctx, opts)
}

// unsafeReviewDeps holds one shadow review of "default"'s echo call that jev
// would approve (min 0.97) and the supervisor denied, at `at`. "default" has
// jev wired in shadow mode at 0.95 / 0.05.
func unsafeReviewDeps(t *testing.T, at time.Time) (Deps, *countingStore) {
	t.Helper()
	at = at.UTC()
	deps := deciderDeps()
	raw, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	store := &countingStore{SQLiteStore: raw}
	for _, ev := range []audit.Event{
		{Timestamp: at, Category: audit.CategorySupervisor, Action: "review", Agent: "default",
			Status: audit.StatusOK, Source: "decider:jev", ConversationID: "c1",
			Detail: `{"tool":"echo","arguments":"{}","decision":"shadow","model":"m","answers":{"off_task":{"type":"noul","noul":0.01},"overreach":{"type":"noul","noul":0.02},"injection":{"type":"noul","noul":0.01},` +
				`"secrets":{"type":"noul","noul":0.03},"sends_external":{"type":"noul","noul":0.02},"private_data":{"type":"noul","noul":0.5}}}`},
		{Timestamp: at.Add(time.Second), Category: audit.CategorySupervisor, Action: "review", Agent: "default",
			Status: audit.StatusOK, Source: "supervisor:argus", ConversationID: "c1",
			Detail: `{"tool":"echo","arguments":"{}","decision":"DENY","supervisor":"argus"}`},
	} {
		if err := raw.Insert(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	deps.AuditStore = store
	deps.Dispatcher.Agent("default").SetSupervisorDecider(stubDecider("jev", deps.CostTracker),
		agent.DeciderStageConfig{Mode: "shadow", ApproveAt: 0.95, DenyAt: 0.05})
	return deps, store
}

func getCounts(t *testing.T, srv *Server) (*httptest.ResponseRecorder, map[string]int) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/decider-reviews/unreviewed"))
	var body map[string]int
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec, body
}

func postMark(t *testing.T, srv *Server, agentName string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentName+"/decider-reviews/reviewed", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func TestUnreviewedCount_CountsThenClearsAfterMark(t *testing.T) {
	at := time.Now().Add(-time.Hour).UTC()
	deps, _ := unsafeReviewDeps(t, at)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec, counts := getCounts(t, srv)
	if rec.Code != http.StatusOK || counts["default"] != 1 {
		t.Fatalf("status %d, counts %v, want default: 1", rec.Code, counts)
	}
	if rec := postMark(t, srv, "default", map[string]string{"up_to": at.Format(time.RFC3339Nano)}); rec.Code != http.StatusOK {
		t.Fatalf("mark status %d: %s", rec.Code, rec.Body.String())
	}
	if _, counts := getCounts(t, srv); counts["default"] != 0 {
		t.Fatalf("counts after mark = %v, want default: 0", counts)
	}
}

func TestUnreviewedCount_LeavesOutEnforceAndUndecided(t *testing.T) {
	deps, _ := unsafeReviewDeps(t, time.Now().Add(-time.Hour))
	enforcing := testDeps().Dispatcher.Agent("default")
	enforcing.SetSupervisorDecider(stubDecider("jev", deps.CostTracker), agent.DeciderStageConfig{Mode: agent.DeciderModeEnforce})
	if err := deps.Dispatcher.AddAgent("enforcer", enforcing); err != nil {
		t.Fatal(err)
	}
	if err := deps.Dispatcher.AddAgent("plain", testDeps().Dispatcher.Agent("default")); err != nil {
		t.Fatal(err)
	}
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	_, counts := getCounts(t, srv)
	if _, ok := counts["enforcer"]; ok {
		t.Errorf("enforce-mode agent listed: %v", counts)
	}
	if _, ok := counts["plain"]; ok {
		t.Errorf("agent without a decider listed: %v", counts)
	}
	if _, ok := counts["default"]; !ok {
		t.Errorf("shadow agent missing: %v", counts)
	}
}

func TestUnreviewedCount_MemoizedUntilMark(t *testing.T) {
	at := time.Now().Add(-time.Hour).UTC()
	deps, store := unsafeReviewDeps(t, at)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	getCounts(t, srv)
	first := store.lists.Load()
	getCounts(t, srv)
	if store.lists.Load() != first {
		t.Fatalf("second call inside the TTL listed the store again (%d -> %d)", first, store.lists.Load())
	}
	postMark(t, srv, "default", map[string]string{"up_to": at.Format(time.RFC3339Nano)})
	getCounts(t, srv)
	if store.lists.Load() == first {
		t.Fatal("a mark did not clear the memo")
	}
}

// A count that was already reading when a mark landed must not put its stale
// result back in the cache the mark just cleared.
func TestUnreviewedCount_ACountRacingAMarkDoesNotCacheItsStaleResult(t *testing.T) {
	at := time.Now().Add(-time.Hour).UTC()
	deps, store := unsafeReviewDeps(t, at)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	store.onList = func() {
		if rec := postMark(t, srv, "default", map[string]string{"up_to": at.Format(time.RFC3339Nano)}); rec.Code != http.StatusOK {
			t.Errorf("mark status %d: %s", rec.Code, rec.Body.String())
		}
	}
	getCounts(t, srv) // reads past the mark's moment, so its result is stale

	if _, counts := getCounts(t, srv); counts["default"] != 0 {
		t.Fatalf("counts after the mark = %v, want default: 0 (the stale count was cached)", counts)
	}
}

func TestUnreviewedCount_RequiresAuditRead(t *testing.T) {
	deps, _ := unsafeReviewDeps(t, time.Now().Add(-time.Hour))
	key := config.APIKeyConfig{Name: "chat-only", Key: "dk-test-key", Scopes: []string{"chat"}}
	srv := New(testConfig(key), deps, testLogger())
	if rec, _ := getCounts(t, srv); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

func TestUnreviewedCount_NoAuditStore(t *testing.T) {
	deps, _ := unsafeReviewDeps(t, time.Now().Add(-time.Hour))
	deps.AuditStore = nil
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	if rec, _ := getCounts(t, srv); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}

type markResponse struct {
	Agent        string    `json:"agent"`
	Decider      string    `json:"decider"`
	ReviewedUpTo time.Time `json:"reviewed_up_to"`
	ReviewedBy   string    `json:"reviewed_by"`
}

func TestMarkReviewed_StoresCallerKeyName(t *testing.T) {
	deps, _ := unsafeReviewDeps(t, time.Now().Add(-time.Hour))
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	up := time.Now().Add(-time.Minute).UTC()
	rec := postMark(t, srv, "default", map[string]string{"up_to": up.Format(time.RFC3339Nano)})
	var body markResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body.Agent != "default" || body.Decider != "jev" || body.ReviewedBy != "test" || !body.ReviewedUpTo.Equal(up) {
		t.Fatalf("body = %+v", body)
	}
}

func TestMarkReviewed_BadRequests(t *testing.T) {
	deps, _ := unsafeReviewDeps(t, time.Now().Add(-time.Hour))
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	cases := map[string]map[string]string{
		"missing up_to": {},
		"future up_to":  {"up_to": time.Now().Add(time.Hour).Format(time.RFC3339Nano)},
		"bad up_to":     {"up_to": "yesterday"},
		// UnixNano wraps before 1677; stored, this would block every later mark.
		"ancient up_to": {"up_to": "1000-01-01T00:00:00Z"},
	}
	for name, body := range cases {
		if rec := postMark(t, srv, "default", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	if _, ok, err := deps.AuditStore.ReviewMark(context.Background(), "default", "jev"); err != nil || ok {
		t.Errorf("a bad request stored a mark (ok=%v err=%v)", ok, err)
	}
	plain := testDeps()
	plain.AuditStore = deps.AuditStore
	if rec := postMark(t, New(testConfig(allScopesKey()), plain, testLogger()), "default",
		map[string]string{"up_to": time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}); rec.Code != http.StatusBadRequest {
		t.Errorf("no decider: status %d, want 400", rec.Code)
	}
	if rec := postMark(t, srv, "ghost", map[string]string{"up_to": time.Now().Format(time.RFC3339Nano)}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown agent: status %d, want 404", rec.Code)
	}
}

func TestMarkReviewed_RequiresAgentsWrite(t *testing.T) {
	deps, _ := unsafeReviewDeps(t, time.Now().Add(-time.Hour))
	key := config.APIKeyConfig{Name: "auditor", Key: "dk-test-key", Scopes: []string{"audit:read"}}
	srv := New(testConfig(key), deps, testLogger())
	if rec := postMark(t, srv, "default", map[string]string{"up_to": time.Now().Format(time.RFC3339Nano)}); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

func TestMarkReviewed_OlderReturnsNewer(t *testing.T) {
	deps, _ := unsafeReviewDeps(t, time.Now().Add(-time.Hour))
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	newer := time.Now().Add(-time.Minute).UTC()
	postMark(t, srv, "default", map[string]string{"up_to": newer.Format(time.RFC3339Nano)})
	rec := postMark(t, srv, "default", map[string]string{"up_to": newer.Add(-time.Hour).Format(time.RFC3339Nano)})
	var body markResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || !body.ReviewedUpTo.Equal(newer) {
		t.Fatalf("status %d, body %+v, want the newer mark", rec.Code, body)
	}
}

func TestDeciderReviews_CarriesTheMark(t *testing.T) {
	deps, _ := unsafeReviewDeps(t, time.Now().Add(-time.Hour))
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents/default/decider-reviews"))
	var before map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &before)
	if v, ok := before["reviewed_up_to"]; !ok || v != nil {
		t.Fatalf("before a mark: reviewed_up_to = %v (present %v), want null", v, ok)
	}
	if _, ok := before["reviewed_by"]; ok {
		t.Fatal("before a mark: reviewed_by present")
	}

	up := time.Now().Add(-time.Minute).UTC()
	postMark(t, srv, "default", map[string]string{"up_to": up.Format(time.RFC3339Nano)})
	rec = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/agents/default/decider-reviews"))
	var after deciderReviewsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.ReviewedUpTo == nil || !after.ReviewedUpTo.Equal(up) || after.ReviewedBy != "test" {
		t.Fatalf("after a mark: %v by %q", after.ReviewedUpTo, after.ReviewedBy)
	}
}
