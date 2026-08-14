package configmcp_test

import (
	"context"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

// recordingWriter stands in for the tracked writer the wiring layer injects.
// It records which mutation reached it, so a handler that still wrote directly
// would show up as a missing call rather than as a silently untracked edit.
type recordingWriter struct {
	mu    sync.Mutex
	calls []string
}

func (w *recordingWriter) record(call string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, call)
}

func (w *recordingWriter) got() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

func (w *recordingWriter) Create(context.Context, string) error {
	w.record("create")
	return nil
}

func (w *recordingWriter) Update(_ context.Context, name, _ string) error {
	w.record("update:" + name)
	return nil
}

func (w *recordingWriter) Rename(_ context.Context, oldName, _ string) error {
	w.record("rename:" + oldName)
	return nil
}

func (w *recordingWriter) Delete(_ context.Context, name string) error {
	w.record("delete:" + name)
	return nil
}

// writerServer wires a config MCP server whose skill mutations all go through
// the recording writer, with just enough skill plumbing for every handler to be
// registered.
func writerServer(t *testing.T) (*recordingWriter, func(name string, args any) (string, bool)) {
	t.Helper()
	writer := &recordingWriter{}
	existing := skill.Skill{Name: "greet", Description: "desc", Version: "1.0.0", Body: "old body"}

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.SkillWriter = writer
		d.GetSkill = func(name string) (skill.Skill, bool) {
			if name == "greet" {
				return existing, true
			}
			return skill.Skill{}, false
		}
		d.UpdateSkill = func(string, skill.Skill) bool { return true }
		d.RemoveSkill = func(string) bool { return true }
	})

	return writer, func(name string, args any) (string, bool) {
		return callTool(t, session, name, args)
	}
}

// TestConfigMCP_SkillMutationsRouteThroughWriter is the canary for the
// invariant the journal depends on: no skill mutation may reach disk without
// passing the tracked writer first.
func TestConfigMCP_SkillMutationsRouteThroughWriter(t *testing.T) {
	writer, call := writerServer(t)

	if text, isErr := call("skill_create", map[string]any{"name": "fresh", "body": "body"}); isErr {
		t.Fatalf("skill_create: %s", text)
	}
	if text, isErr := call("skill_update", map[string]any{"name": "greet", "body": "new body"}); isErr {
		t.Fatalf("skill_update: %s", text)
	}
	if text, isErr := call("skill_update", map[string]any{"name": "greet", "new_name": "renamed"}); isErr {
		t.Fatalf("skill_update rename: %s", text)
	}
	if text, isErr := call("skill_patch", map[string]any{
		"name": "greet", "old_string": "old body", "new_string": "patched body",
	}); isErr {
		t.Fatalf("skill_patch: %s", text)
	}
	if text, isErr := call("skill_delete", map[string]any{"name": "greet"}); isErr {
		t.Fatalf("skill_delete: %s", text)
	}

	want := []string{"create", "update:greet", "rename:greet", "update:greet", "delete:greet"}
	got := writer.got()
	if len(got) != len(want) {
		t.Fatalf("writer saw %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
