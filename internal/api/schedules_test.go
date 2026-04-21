package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

func TestCreateSchedule_Success(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null" // non-persistent for tests
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{
		"name":"test-sched",
		"schedule":"@every 5m",
		"channel":"telegram:123",
		"skill":"greet",
		"enabled":true
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// Verify schedule was registered.
	_, ok := deps.Scheduler.GetEntry("test-sched")
	if !ok {
		t.Error("schedule was not registered in the scheduler")
	}
}

func TestCreateSchedule_MissingName(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"schedule":"@daily","channel":"telegram:123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateSchedule_InvalidExpression(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"name":"bad","schedule":"not-valid","channel":"telegram:123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateSchedule_InvalidChannel(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"name":"bad","schedule":"@daily","channel":"nocolon"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateSchedule_MissingSkill(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{
		"name":"bad-skill-sched",
		"schedule":"@daily",
		"channel":"telegram:123",
		"skill":"nonexistent-skill"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "nonexistent-skill") {
		t.Errorf("expected error to mention skill name; body: %s", rec.Body.String())
	}
}

func TestCreateSchedule_NilScheduler(t *testing.T) {
	deps := testDeps()
	deps.Scheduler = nil
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"name":"x","schedule":"@daily","channel":"telegram:1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestUpdateSchedule_Success(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	// Register a schedule first.
	_ = deps.Scheduler.Register(scheduler.Config{
		Name:     "update-me",
		Type:     "agent",
		Schedule: "@daily",
		Channel:  "telegram:123",
		Enabled:  true,
	}, func(_ scheduler.Entry) {})

	body := `{"schedule":"@hourly"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/schedules/update-me", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestUpdateSchedule_MissingSkill(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	// Register a schedule with a valid skill first.
	_ = deps.Scheduler.Register(scheduler.Config{
		Name:     "update-skill",
		Type:     "agent",
		Schedule: "@daily",
		Skill:    "greet",
		Channel:  "telegram:123",
		Enabled:  true,
	}, func(_ scheduler.Entry) {})

	// Try to update it to reference a nonexistent skill.
	body := `{"skill":"nonexistent-skill"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/schedules/update-skill", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "nonexistent-skill") {
		t.Errorf("expected error to mention skill name; body: %s", rec.Body.String())
	}

	// Original schedule should still be intact.
	entry, ok := deps.Scheduler.GetEntry("update-skill")
	if !ok {
		t.Fatal("schedule should still exist after failed update")
	}
	if entry.Skill != "greet" {
		t.Errorf("skill = %q, want greet (should be unchanged)", entry.Skill)
	}
}

func TestUpdateSchedule_NotFound(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"schedule":"@hourly"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/schedules/nonexistent", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteSchedule_Success(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	_ = deps.Scheduler.Register(scheduler.Config{
		Name:     "delete-me",
		Type:     "agent",
		Schedule: "@daily",
		Channel:  "telegram:123",
		Enabled:  true,
	}, func(_ scheduler.Entry) {})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/schedules/delete-me", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	_, ok := deps.Scheduler.GetEntry("delete-me")
	if ok {
		t.Error("schedule was not unregistered")
	}
}

func TestDeleteSchedule_NotFound(t *testing.T) {
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/schedules/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestScheduleEndpoints_RequiresScope(t *testing.T) {
	readOnlyKey := config.APIKeyConfig{
		Name:   "read-only",
		Key:    "dk-readonly",
		Scopes: []string{"schedules:read"},
	}
	deps := testDeps()
	deps.ConfigPath = "/dev/null"
	srv := New(testConfig(readOnlyKey), deps, testLogger())

	body := `{"name":"x","schedule":"@daily","channel":"telegram:1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-readonly")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for missing schedules:write scope, got %d", rec.Code)
	}
}
