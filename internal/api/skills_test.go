package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
)

// testDepsWithSkillsDir returns deps with the default engine's SkillsDir set.
func testDepsWithSkillsDir(t *testing.T) Deps {
	t.Helper()
	deps := testDeps()
	dir := t.TempDir()
	deps.Dispatcher.Agent("default").SetSkillDirs(dir, "")
	return deps
}

func TestGetSkill_Success(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skills/default/greet", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"greet"`) {
		t.Errorf("response missing skill name: %s", rec.Body.String())
	}
}

func TestGetSkill_NotFound(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skills/default/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetSkill_AgentNotFound(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skills/no-such-agent/greet", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCreateSkill_MissingName(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"body":"some content"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreateSkill_MissingBody(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"name":"test-skill"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestCreateSkill_AgentNotFound(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"name":"test-skill","body":"# Hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/no-such-agent", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestUpdateSkill_NotFound(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"body":"updated content"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/skills/default/nonexistent", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteSkill_NotFound(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/default/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteSkill_AgentNotFound(t *testing.T) {
	deps := testDeps()
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/no-such-agent/greet", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCreateSkill_Success(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"name":"new-skill","description":"A test skill","body":"# Test\nHello world"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"created"`) {
		t.Errorf("response missing status:created: %s", rec.Body.String())
	}

	// Verify the skill is now accessible via GET.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/skills/default/new-skill", nil)
	req2.Header.Set("Authorization", "Bearer dk-test-key")
	rec2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("GET after create: status = %d, want %d; body: %s", rec2.Code, http.StatusOK, rec2.Body.String())
	}
}

func TestCreateSkill_NoSkillsDir(t *testing.T) {
	deps := testDeps() // no SetSkillDirs — SkillsDir() returns ""
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	body := `{"name":"x","body":"# Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestUpdateSkill_Success(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	// First create a skill.
	createBody := `{"name":"editable","body":"# Original"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// Now update it.
	updateBody := `{"body":"# Updated\nNew content","version":"2.0"}`
	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/skills/default/editable", strings.NewReader(updateBody))
	req2.Header.Set("Authorization", "Bearer dk-test-key")
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("update: status = %d, want %d; body: %s", rec2.Code, http.StatusOK, rec2.Body.String())
	}
}

func TestDeleteSkill_Success(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	// First create a skill.
	createBody := `{"name":"deletable","body":"# To be deleted"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// Now delete it.
	req2 := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/default/deletable", nil)
	req2.Header.Set("Authorization", "Bearer dk-test-key")
	rec2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNoContent {
		t.Errorf("delete: status = %d, want %d", rec2.Code, http.StatusNoContent)
	}

	// Verify the skill is gone.
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/skills/default/deletable", nil)
	req3.Header.Set("Authorization", "Bearer dk-test-key")
	rec3 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusNotFound {
		t.Errorf("GET after delete: status = %d, want %d", rec3.Code, http.StatusNotFound)
	}
}

func TestSkillEndpoints_RequiresScope(t *testing.T) {
	readOnlyKey := config.APIKeyConfig{
		Name:   "read-only",
		Key:    "dk-readonly",
		Scopes: []string{"skills:read"},
	}
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(readOnlyKey), deps, testLogger())

	body := `{"name":"x","body":"# Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-readonly")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing skills:write scope, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
