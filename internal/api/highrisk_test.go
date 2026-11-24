package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/approval"
)

// submitHighRiskCall submits a tool_call request the way the engine does:
// the summary names the tool and the payload is the raw arguments.
func submitHighRiskCall(t *testing.T, mgr *approval.Manager, toolName, args string) *approval.Request {
	t.Helper()
	req, err := mgr.Submit(context.Background(), "default", approval.ActionKindToolCall,
		`Execute tool "`+toolName+`" with args: `+args, args, "chat-123", "api", "conv-abc",
		func(_ context.Context, _ string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func serveRequest(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func requireApprovalStatus(t *testing.T, mgr *approval.Manager, id string, want approval.Status) {
	t.Helper()
	got, err := mgr.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != want {
		t.Errorf("status = %q, want %q", got.Status, want)
	}
}

func requireNoRules(t *testing.T, mgr *approval.Manager) {
	t.Helper()
	rules, err := mgr.ListAutoApproveRules(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListAutoApproveRules: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("rules = %+v, want none", rules)
	}
}

// A standing rule is refused before the request resolves, so it stays
// pending for a plain approve or deny.
func TestApproveWithAutoApprove_HighRiskRefusedAndStillPending(t *testing.T) {
	for _, scope := range []string{"permanent", "session"} {
		t.Run(scope, func(t *testing.T) {
			deps := testDeps()
			srv := New(testConfig(allScopesKey()), deps, testLogger())
			req := submitHighRiskCall(t, deps.Approvals, "set_fallback", `{"rules":[]}`)

			rec := serveRequest(srv, authedRequest(http.MethodPost, "/api/v1/approvals/"+req.ID+"/approve?auto_approve="+scope))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "high-risk call and cannot be auto-approved") {
				t.Fatalf("status = %d, body %s; want 400 naming the high-risk refusal", rec.Code, rec.Body)
			}
			requireApprovalStatus(t, deps.Approvals, req.ID, approval.StatusPending)
			requireNoRules(t, deps.Approvals)
		})
	}
}

func TestApprove_HighRiskWithoutAutoApproveApprovesOnce(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	req := submitHighRiskCall(t, deps.Approvals, "set_fallback", `{"rules":[]}`)

	rec := serveRequest(srv, authedRequest(http.MethodPost, "/api/v1/approvals/"+req.ID+"/approve"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body)
	}
	requireApprovalStatus(t, deps.Approvals, req.ID, approval.StatusApproved)
	requireNoRules(t, deps.Approvals)
}

func TestApproveWithAutoApprove_HarmlessCallCreatesRule(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	req := submitHighRiskCall(t, deps.Approvals, "schedule_update", `{"name":"nightly","schedule":"@daily"}`)

	rec := serveRequest(srv, authedRequest(http.MethodPost, "/api/v1/approvals/"+req.ID+"/approve?auto_approve=permanent"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body)
	}
	rules, err := deps.Approvals.ListAutoApproveRules(context.Background(), "default")
	if err != nil || len(rules) != 1 || rules[0].ToolName != "schedule_update" {
		t.Fatalf("rules = %+v, %v; want one schedule_update rule", rules, err)
	}
}

func TestCreateAutoApprove_HighRiskToolRefused(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	for _, body := range []string{
		`{"agent":"default","tool":"set_fallback","scope":"permanent"}`,
		`{"agent":"default","tool":"set_fallback","scope":"session","conversation_id":"conv-1"}`,
		`{"agent":"default","tool":"tool_add","scope":"permanent"}`,
	} {
		rec := postJSON(srv, "/api/v1/auto-approve", body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "is high-risk and cannot be auto-approved") {
			t.Errorf("%s: status = %d, body %s; want 400", body, rec.Code, rec.Body)
		}
	}
	rec := postJSON(srv, "/api/v1/auto-approve", `{"agent":"default","tool":"schedule_update","scope":"permanent"}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("schedule_update: status = %d, body %s; want 201", rec.Code, rec.Body)
	}
}

func TestListApprovals_CarriesRisk(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	submitHighRiskCall(t, deps.Approvals, "schedule_update", `{"name":"nightly","session_tier":"autonomous"}`)

	rec := serveRequest(srv, authedRequest(http.MethodGet, "/api/v1/approvals?status=pending"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var rows []struct {
		Risk *struct {
			Level   string `json:"level"`
			Flagged []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"flagged"`
		} `json:"risk"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Risk == nil || rows[0].Risk.Level != "high" ||
		rows[0].Risk.Flagged[0].Name != "session_tier" || rows[0].Risk.Flagged[0].Value != `"autonomous"` {
		t.Fatalf("rows = %s", rec.Body)
	}
}

func TestListAutoApprove_MarksStoredHighRiskRuleInert(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	deps.Approvals.SetConfigRules(context.Background(), map[string][]string{"default": {"set_fallback"}})

	rec := serveRequest(srv, authedRequest(http.MethodGet, "/api/v1/auto-approve"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"inert":true`) {
		t.Fatalf("status = %d, body %s; want the set_fallback rule marked inert", rec.Code, rec.Body)
	}
}
