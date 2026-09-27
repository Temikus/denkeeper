package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/scheduler"
)

func lintSkillRequest(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func decodeWarnings(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var resp struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v; body: %s", err, rec.Body.String())
	}
	return resp.Warnings
}

func TestCreateSkill_ScheduleTriggerForMissingSchedule_Rejected(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithSkillsDir(t), testLogger())

	rec := lintSkillRequest(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"review","body":"Review skills.","triggers":["schedule:weekly-review-sun-9am"]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "weekly-review-sun-9am") {
		t.Errorf("error should name the missing schedule: %s", rec.Body.String())
	}
}

func TestCreateSkill_ScheduledWithoutRoundCap_Warns(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	if err := deps.Scheduler.Register(scheduler.Config{
		Name: "weekly-review", Type: string(scheduler.ScheduleTypeAgent),
		Agent: "default", Schedule: "@weekly", Skill: "review",
	}, func(scheduler.Entry) {}); err != nil {
		t.Fatalf("registering schedule: %v", err)
	}
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	rec := lintSkillRequest(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"review","body":"Review skills.","triggers":["schedule:weekly-review"]}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if w := decodeWarnings(t, rec); len(w) != 1 || !strings.Contains(w[0], "max_tool_rounds") {
		t.Errorf("warnings = %q, want one max_tool_rounds warning", w)
	}
}

func TestUpdateSkill_ScheduleTriggerForMissingSchedule_Rejected(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithSkillsDir(t), testLogger())
	if rec := lintSkillRequest(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"review","body":"Review skills."}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	rec := lintSkillRequest(t, srv, http.MethodPut, "/api/v1/skills/default/review",
		`{"triggers":["schedule:weekly-review-sun-9am"]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestUpdateSkill_BodyChangedVersionKept_Warns(t *testing.T) {
	srv := New(testConfig(allScopesKey()), testDepsWithSkillsDir(t), testLogger())
	if rec := lintSkillRequest(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"review","body":"Review skills."}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; body: %s", rec.Code, rec.Body.String())
	}

	rec := lintSkillRequest(t, srv, http.MethodPut, "/api/v1/skills/default/review",
		`{"body":"Review skills carefully."}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if w := decodeWarnings(t, rec); len(w) != 1 || !strings.Contains(w[0], "version") {
		t.Errorf("warnings = %q, want one version warning", w)
	}
}
