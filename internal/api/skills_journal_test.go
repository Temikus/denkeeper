package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
)

// revisionStore pulls the concrete store out of Deps so a test can read the
// undo journal the REST handlers write.
func revisionStore(t *testing.T, deps Deps) *agent.SQLiteMemoryStore {
	t.Helper()
	store, ok := deps.Memory.(*agent.SQLiteMemoryStore)
	if !ok {
		t.Fatalf("test deps memory is %T, want *agent.SQLiteMemoryStore", deps.Memory)
	}
	return store
}

func doJSON(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dk-test-key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

// TestSkillJournal_UpdateViaAPIRecordsPriorBytes proves the REST surface is
// behind the same journal as the agent's own tools: an operator edit is just as
// revertible as one the agent made itself.
func TestSkillJournal_UpdateViaAPIRecordsPriorBytes(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	skillsDir := deps.Dispatcher.Agent("default").SkillsDir()

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"journaled","version":"1.0.0","body":"first body"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body: %s", rec.Code, rec.Body.String())
	}
	before, err := os.ReadFile(filepath.Join(skillsDir, "journaled.md"))
	if err != nil {
		t.Fatal(err)
	}

	if rec := doJSON(t, srv, http.MethodPut, "/api/v1/skills/default/journaled",
		`{"version":"2.0.0","body":"second body"}`); rec.Code != http.StatusOK {
		t.Fatalf("update status = %d; body: %s", rec.Code, rec.Body.String())
	}

	rev, err := revisionStore(t, deps).LatestSkillRevision(context.Background(), "default", "journaled")
	if err != nil {
		t.Fatalf("reading revision: %v", err)
	}
	if rev == nil {
		t.Fatal("the REST update was not journaled")
	}
	if rev.Op != agent.SkillOpUpdate {
		t.Errorf("op = %q, want %q", rev.Op, agent.SkillOpUpdate)
	}
	if rev.Actor != agent.SkillActorUser {
		t.Errorf("actor = %q, want %q (REST is the operator's surface)", rev.Actor, agent.SkillActorUser)
	}
	if rev.PriorPayload == nil || *rev.PriorPayload != string(before) {
		t.Errorf("prior payload is not the exact prior bytes: %v", rev.PriorPayload)
	}
	if rev.PriorVersion != "1.0.0" || rev.NewVersion != "2.0.0" {
		t.Errorf("versions = (%q, %q), want (1.0.0, 2.0.0)", rev.PriorVersion, rev.NewVersion)
	}
}

// TestSkillJournal_DeleteViaAPIRecordsPriorBytes covers the op whose undo needs
// the bytes most: after a delete there is nothing left on disk to read.
func TestSkillJournal_DeleteViaAPIRecordsPriorBytes(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())
	skillsDir := deps.Dispatcher.Agent("default").SkillsDir()

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"doomed","version":"1.0.0","body":"body"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body: %s", rec.Code, rec.Body.String())
	}
	before, err := os.ReadFile(filepath.Join(skillsDir, "doomed.md"))
	if err != nil {
		t.Fatal(err)
	}

	if rec := doJSON(t, srv, http.MethodDelete, "/api/v1/skills/default/doomed", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; body: %s", rec.Code, rec.Body.String())
	}
	if _, statErr := os.Stat(filepath.Join(skillsDir, "doomed.md")); !os.IsNotExist(statErr) {
		t.Error("skill file should be gone after delete")
	}

	rev, err := revisionStore(t, deps).LatestSkillRevision(context.Background(), "default", "doomed")
	if err != nil {
		t.Fatalf("reading revision: %v", err)
	}
	if rev == nil {
		t.Fatal("the REST delete was not journaled")
	}
	if rev.Op != agent.SkillOpDelete {
		t.Errorf("op = %q, want %q", rev.Op, agent.SkillOpDelete)
	}
	if rev.PriorPayload == nil || *rev.PriorPayload != string(before) {
		t.Errorf("deleted skill's bytes were not captured: %v", rev.PriorPayload)
	}
}

// TestSkillJournal_RenameViaAPIRecordsOldName keeps the rename inverse
// addressable: without prior_name there is nothing to rename back to.
func TestSkillJournal_RenameViaAPIRecordsOldName(t *testing.T) {
	deps := testDepsWithSkillsDir(t)
	srv := New(testConfig(allScopesKey()), deps, testLogger())

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/skills/default",
		`{"name":"before-name","version":"1.0.0","body":"body"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, srv, http.MethodPut, "/api/v1/skills/default/before-name",
		`{"name":"after-name"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d; body: %s", rec.Code, rec.Body.String())
	}

	rev, err := revisionStore(t, deps).LatestSkillRevision(context.Background(), "default", "after-name")
	if err != nil {
		t.Fatalf("reading revision: %v", err)
	}
	if rev == nil {
		t.Fatal("the REST rename was not journaled")
	}
	if rev.Op != agent.SkillOpRename {
		t.Errorf("op = %q, want %q", rev.Op, agent.SkillOpRename)
	}
	if rev.PriorName == nil || *rev.PriorName != "before-name" {
		t.Errorf("prior name = %v, want before-name", rev.PriorName)
	}
}
