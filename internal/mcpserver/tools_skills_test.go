package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// toolResultText extracts the first text content from a tool result, for
// surfacing error messages in test failures.
func toolResultText(r *mcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	if tc, ok := r.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// skillServer wires a Server whose "test-agent" engine has its skills directory
// pointed at dir, so the skill write handlers persist there.
func skillServer(t *testing.T, dir string) (*Server, *agent.Engine) {
	t.Helper()
	e := testEngine(t)
	e.SetSkillDirs(dir, "")
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{"test-agent": e}, nil, nil, testLogger())
	s := &Server{deps: Deps{Dispatcher: dispatcher, Logger: testLogger()}}
	return s, e
}

func writeScope(t *testing.T) context.Context {
	t.Helper()
	return withScopes(context.Background(), []string{"skills:write"})
}

func TestSkillCreate_PersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	s, e := skillServer(t, dir)

	res, _, err := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent:       "test-agent",
		Name:        "greet",
		Description: "Greeting skill",
		Version:     "1.0.0",
		Body:        "Say hello.",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}

	data, readErr := os.ReadFile(filepath.Join(dir, "greet.md"))
	if readErr != nil {
		t.Fatalf("reading persisted skill: %v", readErr)
	}
	content := string(data)
	if !strings.Contains(content, "Say hello.") {
		t.Errorf("persisted file missing body: %s", content)
	}
	if !strings.Contains(content, `version = '1.0.0'`) {
		t.Errorf("persisted file missing version: %s", content)
	}

	if _, ok := e.GetSkill("greet"); !ok {
		t.Error("skill not registered in memory")
	}
}

func TestSkillUpdate_PersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	s, e := skillServer(t, dir)

	// Seed an existing skill on disk + in memory via the create handler.
	if res, _, _ := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: "greet", Version: "1.0.0", Body: "old body",
	}); res.IsError {
		t.Fatalf("seed create failed: %s", toolResultText(res))
	}

	newBody := "brand new body"
	newVersion := "2.0.0"
	res, _, err := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{
		Agent:   "test-agent",
		Name:    "greet",
		Version: &newVersion,
		Body:    &newBody,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}

	data, readErr := os.ReadFile(filepath.Join(dir, "greet.md"))
	if readErr != nil {
		t.Fatalf("reading persisted skill: %v", readErr)
	}
	content := string(data)
	if !strings.Contains(content, newBody) {
		t.Errorf("persisted file missing new body: %s", content)
	}
	if !strings.Contains(content, `version = '2.0.0'`) {
		t.Errorf("persisted file missing new version: %s", content)
	}

	sk, ok := e.GetSkill("greet")
	if !ok {
		t.Fatal("skill missing from memory after update")
	}
	if sk.Body != newBody || sk.Version != newVersion {
		t.Errorf("in-memory skill not updated: body=%q version=%q", sk.Body, sk.Version)
	}
}

func TestSkillUpdate_VersionField(t *testing.T) {
	dir := t.TempDir()
	s, _ := skillServer(t, dir)

	if res, _, _ := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: "greet", Body: "body",
	}); res.IsError {
		t.Fatalf("seed create failed: %s", toolResultText(res))
	}

	version := "3.1.4"
	res, _, err := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{
		Agent: "test-agent", Name: "greet", Version: &version,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}

	data, _ := os.ReadFile(filepath.Join(dir, "greet.md"))
	if !strings.Contains(string(data), `version = '3.1.4'`) {
		t.Errorf("version field not persisted to frontmatter: %s", string(data))
	}
}

func TestSkillUpdate_Rename(t *testing.T) {
	dir := t.TempDir()
	s, e := skillServer(t, dir)

	if res, _, _ := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: "old", Body: "body",
	}); res.IsError {
		t.Fatalf("seed create failed: %s", toolResultText(res))
	}

	newName := "new"
	res, _, err := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{
		Agent: "test-agent", Name: "old", NewName: &newName,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}

	if _, err := os.Stat(filepath.Join(dir, "old.md")); !os.IsNotExist(err) {
		t.Error("old skill file should be removed after rename")
	}
	if _, err := os.Stat(filepath.Join(dir, "new.md")); err != nil {
		t.Errorf("new skill file missing after rename: %v", err)
	}
	if _, ok := e.GetSkill("old"); ok {
		t.Error("old skill still in memory after rename")
	}
	if _, ok := e.GetSkill("new"); !ok {
		t.Error("new skill missing from memory after rename")
	}
}

func TestSkillDelete_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	s, e := skillServer(t, dir)

	if res, _, _ := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: "greet", Body: "body",
	}); res.IsError {
		t.Fatalf("seed create failed: %s", toolResultText(res))
	}

	res, _, err := s.handleSkillDelete(writeScope(t), nil, skillDeleteInput{
		Agent: "test-agent", Name: "greet",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}

	if _, statErr := os.Stat(filepath.Join(dir, "greet.md")); !os.IsNotExist(statErr) {
		t.Error("skill file should be removed from disk after delete")
	}
	if _, ok := e.GetSkill("greet"); ok {
		t.Error("skill still in memory after delete")
	}

	// Second delete on the now-missing skill: not found in memory → tool error,
	// but no panic and no hard error from the (already absent) file.
	res2, _, _ := s.handleSkillDelete(writeScope(t), nil, skillDeleteInput{
		Agent: "test-agent", Name: "greet",
	})
	if !res2.IsError {
		t.Error("expected tool error deleting a missing skill")
	}
}

func TestSkillName_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	s, _ := skillServer(t, dir)
	ctx := writeScope(t)

	bad := "../escape"

	if res, _, _ := s.handleSkillCreate(ctx, nil, skillCreateInput{
		Agent: "test-agent", Name: bad, Body: "x",
	}); !res.IsError {
		t.Error("create: expected tool error for traversal name")
	}
	if res, _, _ := s.handleSkillDelete(ctx, nil, skillDeleteInput{
		Agent: "test-agent", Name: bad,
	}); !res.IsError {
		t.Error("delete: expected tool error for traversal name")
	}

	// Seed a valid skill, then attempt to rename it to a traversal target.
	if res, _, _ := s.handleSkillCreate(ctx, nil, skillCreateInput{
		Agent: "test-agent", Name: "greet", Body: "x",
	}); res.IsError {
		t.Fatalf("seed create failed: %s", toolResultText(res))
	}
	newName := "../../evil"
	if res, _, _ := s.handleSkillUpdate(ctx, nil, skillUpdateInput{
		Agent: "test-agent", Name: "greet", NewName: &newName,
	}); !res.IsError {
		t.Error("rename: expected tool error for traversal new_name")
	}

	// No stray files were written above the skills directory.
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.md")); !os.IsNotExist(err) {
		t.Error("traversal write escaped the skills directory")
	}
}

func TestSkillCreate_SizeCapRejects(t *testing.T) {
	dir := t.TempDir()
	e := testEngine(t)
	e.SetSkillDirs(dir, "")
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{"test-agent": e}, nil, nil, testLogger())
	s := &Server{deps: Deps{
		Dispatcher: dispatcher,
		Logger:     testLogger(),
		Config:     &config.Config{Skills: config.SkillsConfig{MaxBytes: 512}},
	}}

	res, _, err := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: "greet", Body: strings.Repeat("x", 2000),
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected tool error for over-cap skill body")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "greet.md")); !os.IsNotExist(statErr) {
		t.Error("over-cap skill must not be written to disk")
	}
	if _, ok := e.GetSkill("greet"); ok {
		t.Error("over-cap skill must not be registered in memory")
	}
}

func TestSkillUpdate_NoSkillsDir(t *testing.T) {
	e := testEngine(t) // no SetSkillDirs → empty skills dir
	dispatcher := agent.NewDispatcher(map[string]*agent.Engine{"test-agent": e}, nil, nil, testLogger())
	s := &Server{deps: Deps{Dispatcher: dispatcher, Logger: testLogger()}}

	body := "x"
	res, _, err := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{
		Agent: "test-agent", Name: "greet", Body: &body,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Error("expected tool error when skills dir is unavailable")
	}
}

func TestSkillUpdate_PersistFailure(t *testing.T) {
	// Point the skills dir at a path that is actually a regular file, so
	// MkdirAll/OpenRoot fail and the disk-first write errors before any
	// in-memory mutation happens.
	badDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(badDir, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	s, e := skillServer(t, badDir)

	// Seed the skill in memory only (bypassing disk).
	e.AppendSkill(skill.Skill{Name: "greet", Version: "1.0.0", Body: "original"})

	newBody := "should not persist"
	res, _, err := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{
		Agent: "test-agent", Name: "greet", Body: &newBody,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool error on persist failure")
	}

	// In-memory state must be unchanged (disk-first atomicity).
	sk, ok := e.GetSkill("greet")
	if !ok {
		t.Fatal("skill vanished from memory after failed persist")
	}
	if sk.Body != "original" {
		t.Errorf("in-memory body mutated despite persist failure: %q", sk.Body)
	}
}
