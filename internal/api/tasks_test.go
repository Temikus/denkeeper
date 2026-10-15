package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/task"
)

// testDepsWithTasks returns deps with a real in-memory task service whose
// members are the "default" den agent and a claude-code key.
func testDepsWithTasks(t *testing.T) Deps {
	t.Helper()
	deps := testDeps()
	store, err := task.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	dir := task.DirectoryFunc(func(context.Context) ([]task.Member, error) {
		return []task.Member{
			{ID: task.AgentID("default"), Name: "default", Kind: "agent"},
			{ID: task.ExternalID("claude-code"), Name: "claude-code", Kind: "external"},
		}, nil
	})
	deps.Tasks = task.NewService(store, dir, nil)
	return deps
}

func doTaskRequest(t *testing.T, srv *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func decodeTask(t *testing.T, rec *httptest.ResponseRecorder) taskView {
	t.Helper()
	var v taskView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding task: %v; body %s", err, rec.Body.String())
	}
	return v
}

func TestCreateTask_Unassigned(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithTasks(t), testLogger())

	rec := doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Renew the domain"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	v := decodeTask(t, rec)
	if v.Key != "DK-1" || v.Assignee != "" || v.Status != task.StatusTodo || v.CreatedBy != task.Human || v.Reviewer != task.Human {
		t.Errorf("created = %+v", v)
	}
}

func TestCreateTask_AssignedByName(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithTasks(t), testLogger())

	rec := doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks", map[string]any{
		"title": "Add Fastmail to the weekly digest", "assignee": "default", "no_review": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	if v := decodeTask(t, rec); v.Assignee != "agent:default" || v.Reviewer != "" {
		t.Errorf("created = %+v", v)
	}
}

func TestCreateTask_Invalid(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithTasks(t), testLogger())

	rec := doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks", map[string]any{"title": ""})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "title is required") {
		t.Errorf("empty title: %d %s", rec.Code, rec.Body.String())
	}
	rec = doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x", "assignee": "quill"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no member named") {
		t.Errorf("unknown assignee: %d %s", rec.Code, rec.Body.String())
	}
}

func TestListTasks_FiltersByAssignee(t *testing.T) {
	deps := testDepsWithTasks(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	ctx := context.Background()
	if _, err := deps.Tasks.Create(ctx, task.Human, task.CreateRequest{Title: "for default", Assignee: "default"}); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Tasks.Create(ctx, task.Human, task.CreateRequest{Title: "for nobody"}); err != nil {
		t.Fatal(err)
	}

	rec := doTaskRequest(t, srv, http.MethodGet, "/api/v1/tasks?assignee=default", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Tasks []taskView `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tasks) != 1 || out.Tasks[0].Title != "for default" || out.Tasks[0].Key != "DK-1" {
		t.Errorf("tasks = %+v", out.Tasks)
	}
}

func TestListTasks_BadFilter(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithTasks(t), testLogger())
	for _, q := range []string{"status=paused", "closed_within=soon", "parent=x", "limit=-1"} {
		rec := doTaskRequest(t, srv, http.MethodGet, "/api/v1/tasks?"+q, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", q, rec.Code)
		}
	}
}

func TestGetTask_DetailByKey(t *testing.T) {
	deps := testDepsWithTasks(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	ctx := context.Background()
	parent, _ := deps.Tasks.Create(ctx, task.Human, task.CreateRequest{Title: "parent", Assignee: "default"})
	if _, err := deps.Tasks.Create(ctx, task.AgentID("default"), task.CreateRequest{Title: "child", Assignee: "claude-code", ParentID: &parent.ID}); err != nil {
		t.Fatal(err)
	}

	rec := doTaskRequest(t, srv, http.MethodGet, "/api/v1/tasks/dk-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	var d taskDetailView
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Task.Key != "DK-1" || len(d.Children) != 1 || d.Children[0].Key != "DK-2" || len(d.Events) != 2 {
		t.Errorf("detail = %+v", d)
	}
}

func TestGetTask_NotFoundAndBadID(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithTasks(t), testLogger())
	if rec := doTaskRequest(t, srv, http.MethodGet, "/api/v1/tasks/9", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing: status = %d", rec.Code)
	}
	if rec := doTaskRequest(t, srv, http.MethodGet, "/api/v1/tasks/abc", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: status = %d", rec.Code)
	}
}

func TestUpdateTask_StatusAndUnassign(t *testing.T) {
	deps := testDepsWithTasks(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	tk, _ := deps.Tasks.Create(context.Background(), task.Human, task.CreateRequest{Title: "Digest", Assignee: "default"})

	rec := doTaskRequest(t, srv, http.MethodPatch, "/api/v1/tasks/1", map[string]any{"assignee": "", "status": "blocked", "reason": "needs a password"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	v := decodeTask(t, rec)
	if v.ID != tk.ID || v.Assignee != "" || v.Status != task.StatusBlocked || v.BlockedReason != "needs a password" {
		t.Errorf("updated = %+v", v)
	}
	rec = doTaskRequest(t, srv, http.MethodPatch, "/api/v1/tasks/1", map[string]any{"status": "blocked"})
	if rec.Code != http.StatusOK {
		t.Errorf("no-op status change: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReleaseTask_StuckClaim(t *testing.T) {
	deps := testDepsWithTasks(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	ctx := context.Background()
	tk, _ := deps.Tasks.Create(ctx, task.Human, task.CreateRequest{Title: "Rate limits", Assignee: "claude-code"})
	if _, err := deps.Tasks.Checkout(ctx, task.ExternalID("claude-code"), tk.ID); err != nil {
		t.Fatal(err)
	}

	rec := doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks/1/release", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	if v := decodeTask(t, rec); v.Holder != "" || v.Status != task.StatusTodo || v.Assignee != "ext:claude-code" {
		t.Errorf("released = %+v", v)
	}
	rec = doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks/1/release", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("second release: status = %d, want 400", rec.Code)
	}
}

func TestCommentTask(t *testing.T) {
	deps := testDepsWithTasks(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	if _, err := deps.Tasks.Create(context.Background(), task.Human, task.CreateRequest{Title: "Digest"}); err != nil {
		t.Fatal(err)
	}

	rec := doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks/1/comments", map[string]any{"body": "Include labels too."})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	var ev task.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Kind != task.EventComment || ev.Actor != task.Human || ev.Body != "Include labels too." {
		t.Errorf("event = %+v", ev)
	}
}

func TestTaskMembers_HumanFirst(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithTasks(t), testLogger())
	rec := doTaskRequest(t, srv, http.MethodGet, "/api/v1/tasks/members", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Members []task.Member `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Members) != 3 || out.Members[0].ID != task.Human || out.Members[2].ID != "ext:claude-code" {
		t.Errorf("members = %+v", out.Members)
	}
}

func TestTasks_NotConfigured(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDeps(), testLogger())
	if rec := doTaskRequest(t, srv, http.MethodGet, "/api/v1/tasks", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestTasks_ScopeEnforced(t *testing.T) {
	readOnly := config.APIKeyConfig{Name: "reader", Key: "dk-test-key", Scopes: []string{"tasks:read"}}
	srv := New(testConfig(readOnly), testDepsWithTasks(t), testLogger())

	if rec := doTaskRequest(t, srv, http.MethodGet, "/api/v1/tasks", nil); rec.Code != http.StatusOK {
		t.Errorf("read with tasks:read: status = %d", rec.Code)
	}
	if rec := doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x"}); rec.Code != http.StatusForbidden {
		t.Errorf("write with tasks:read: status = %d, want 403", rec.Code)
	}
}

func TestTasks_NonAdminKeyActsAsExternalMember(t *testing.T) {
	harness := config.APIKeyConfig{Name: "claude-code", Key: "dk-test-key", Scopes: []string{"tasks:read", "tasks:write"}}
	deps := testDepsWithTasks(t)
	srv := New(testConfig(harness), deps, testLogger())

	rec := doTaskRequest(t, srv, http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Filed by the harness"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if v := decodeTask(t, rec); v.CreatedBy != "ext:claude-code" {
		t.Errorf("created_by = %q, want ext:claude-code", v.CreatedBy)
	}

	// An operator-reviewed task can't be closed by the harness through REST.
	tk, _ := deps.Tasks.Create(context.Background(), task.Human, task.CreateRequest{Title: "Reviewed", Assignee: "claude-code"})
	if _, err := deps.Tasks.Checkout(context.Background(), task.ExternalID("claude-code"), tk.ID); err != nil {
		t.Fatal(err)
	}
	rec = doTaskRequest(t, srv, http.MethodPatch, "/api/v1/tasks/"+tk.Key(), map[string]any{"status": "done"})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "needs review by human") {
		t.Errorf("harness close: %d %s, want 400 review refusal", rec.Code, rec.Body.String())
	}
}

func TestTasks_InternalErrorIsGeneric(t *testing.T) {
	deps := testDepsWithTasks(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	rec := httptest.NewRecorder()
	srv.writeTaskError(rec, errors.New("committing task transaction: database is locked"))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "database") {
		t.Errorf("500 body = %d %s, want a generic message", rec.Code, rec.Body.String())
	}
}
