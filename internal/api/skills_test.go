package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/skill"
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

func TestRenameSkill_Success(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	// Create a skill.
	createBody := `{"name":"old-name","body":"# Content"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// Rename it.
	renameBody := `{"name":"new-name"}`
	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/skills/default/old-name", strings.NewReader(renameBody))
	req2.Header.Set("Authorization", "Bearer dk-test-key")
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("rename: status = %d, want %d; body: %s", rec2.Code, http.StatusOK, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), `"name":"new-name"`) {
		t.Errorf("response should contain new name: %s", rec2.Body.String())
	}

	// Old name should be gone.
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/skills/default/old-name", nil)
	req3.Header.Set("Authorization", "Bearer dk-test-key")
	rec3 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusNotFound {
		t.Errorf("GET old name: status = %d, want %d", rec3.Code, http.StatusNotFound)
	}

	// New name should be accessible.
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/skills/default/new-name", nil)
	req4.Header.Set("Authorization", "Bearer dk-test-key")
	rec4 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec4, req4)

	if rec4.Code != http.StatusOK {
		t.Errorf("GET new name: status = %d, want %d; body: %s", rec4.Code, http.StatusOK, rec4.Body.String())
	}
}

func TestRenameSkill_Conflict(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	// Create two skills.
	for _, name := range []string{"skill-a", "skill-b"} {
		body := `{"name":"` + name + `","body":"# Content"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/skills/default", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer dk-test-key")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: status = %d", name, rec.Code)
		}
	}

	// Try to rename skill-a to skill-b (conflict).
	renameBody := `{"name":"skill-b"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/skills/default/skill-a", strings.NewReader(renameBody))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("rename to existing: status = %d, want %d; body: %s", rec.Code, http.StatusConflict, rec.Body.String())
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

func TestDeleteSkill_FileRemovalFailure(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	e := deps.Dispatcher.Agent("default")
	dir := e.SkillsDir()

	// Seed the skill in memory and place a NON-EMPTY directory where its file
	// would be, so the confined Remove fails with a real (non-NotExist) error.
	e.AppendSkill(skill.Skill{Name: "stuck", Body: "x"})
	clash := filepath.Join(dir, "stuck.md")
	if err := os.Mkdir(clash, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clash, "child"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/skills/default/stuck", nil)
	req.Header.Set("Authorization", "Bearer dk-test-key")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	// Disk-first: a real file-removal error is fatal (500), not a silent 204.
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d; body: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	// ... and the skill must remain in memory.
	if _, ok := e.GetSkill("stuck"); !ok {
		t.Error("skill must remain in memory when file removal fails")
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

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for missing skills:write scope, got %d; body: %s", rec.Code, rec.Body.String())
	}
}
