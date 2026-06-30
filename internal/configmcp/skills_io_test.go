package configmcp_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

func ioTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestApplySkillCreate_PersistsInsideRoot is the happy path: a normal skill
// name writes "<name>.md" inside the skills directory.
func TestApplySkillCreate_PersistsInsideRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	payload := configmcp.BuildSkillPayload("greet", "desc", "1.0.0", nil, "hello body")

	if err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {}, ioTestLogger(), payload, 0); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "greet.md")); err != nil {
		t.Errorf("expected skill file inside skills dir: %v", err)
	}
}

// TestApplySkillCreate_SizeCapRejects proves an over-limit payload is rejected
// before any write, and the in-memory append callback is not invoked.
func TestApplySkillCreate_SizeCapRejects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	big := strings.Repeat("x", 5000)
	payload := configmcp.BuildSkillPayload("greet", "", "1.0.0", nil, big)

	err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {
		t.Error("appendSkill must not be called when the payload is over the cap")
	}, ioTestLogger(), payload, 1024)
	if err == nil {
		t.Fatal("expected error for over-limit payload")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "greet.md")); !os.IsNotExist(statErr) {
		t.Error("no file should be written when the payload is rejected")
	}
}

// TestApplySkillCreate_SizeCapAllowsUnderLimit confirms a payload under the cap
// (and the unlimited maxBytes<=0 case) is written normally.
func TestApplySkillCreate_SizeCapAllowsUnderLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	payload := configmcp.BuildSkillPayload("greet", "", "1.0.0", nil, "small body")

	if err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {}, ioTestLogger(), payload, 1<<20); err != nil {
		t.Fatalf("under-limit create failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "greet.md")); err != nil {
		t.Errorf("expected skill file: %v", err)
	}
}

// TestApplySkillCreate_NoTempLeftBehind confirms the randomized temp file is
// renamed away (or cleaned up), leaving only the final "<name>.md".
func TestApplySkillCreate_NoTempLeftBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	payload := configmcp.BuildSkillPayload("greet", "", "1.0.0", nil, "body")
	if err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {}, ioTestLogger(), payload, 0); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "greet.md" {
			t.Errorf("unexpected leftover file in skills dir: %s", e.Name())
		}
	}
}

// TestApplySkillCreate_ConfinedToRoot proves the end-to-end invariant: a
// traversal name injected directly into the payload frontmatter is rejected
// (by ValidateSkillName and/or the os.Root boundary) and nothing escapes the
// skills directory. See TestWriteSkillFileAtomic_RootBackstop (white-box) for
// the proof that os.Root alone confines it when validation is bypassed.
func TestApplySkillCreate_ConfinedToRoot(t *testing.T) {
	base := t.TempDir()
	skillsDir := filepath.Join(base, "skills")

	payload := configmcp.BuildSkillPayload("../escape", "", "", nil, "evil body")

	err := configmcp.ApplySkillCreate(skillsDir, func(skill.Skill) {
		t.Error("appendSkill must not be called when the disk write is rejected")
	}, ioTestLogger(), payload, 0)
	if err == nil {
		t.Fatal("expected error: traversal write should be rejected by os.Root")
	}

	// Nothing escaped to the parent directory.
	for _, name := range []string{"escape.md", "escape.md.tmp"} {
		if _, statErr := os.Stat(filepath.Join(base, name)); !os.IsNotExist(statErr) {
			t.Errorf("traversal write escaped the skills directory: %s exists", name)
		}
	}
}

// TestApplySkillUpdate_ConcurrentSameNameNoCorruption stresses the randomized
// temp file: many writers updating the same skill concurrently must each end
// with a fully-written file (one of the payloads), never a torn/interleaved one.
// Run with -race.
func TestApplySkillUpdate_ConcurrentSameNameNoCorruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	const writers = 16

	var wg sync.WaitGroup
	valid := make(map[string]struct{}, writers)
	for i := 0; i < writers; i++ {
		body := "body-" + strings.Repeat(string(rune('a'+i)), 2000)
		valid[body] = struct{}{}
		payload := configmcp.BuildSkillPayload("greet", "", "1.0.0", nil, body)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = configmcp.ApplySkillUpdate(dir, func(string, skill.Skill) bool { return true }, ioTestLogger(), "greet", payload, 0)
		}()
	}
	wg.Wait()

	// The surviving file must parse and its body must be exactly one of the
	// payloads written — proving no two writers interleaved into one file.
	data, err := os.ReadFile(filepath.Join(dir, "greet.md"))
	if err != nil {
		t.Fatalf("reading final skill: %v", err)
	}
	parsed, err := skill.ParseFile("greet.md", data)
	if err != nil {
		t.Fatalf("final skill file is corrupt / unparseable: %v", err)
	}
	if _, ok := valid[parsed.Body]; !ok {
		t.Errorf("final body is not any single writer's payload (torn write?): %q", parsed.Body)
	}

	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "greet.md" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestRemoveSkillFile_RemovesInsideRoot is the happy path for confined removal.
func TestRemoveSkillFile_RemovesInsideRoot(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "greet.md")
	if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := configmcp.RemoveSkillFile(dir, "greet"); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("expected skill file to be removed")
	}
}

// TestRemoveSkillFile_ConfinedToRoot proves a traversal name cannot delete a
// file outside the skills directory.
func TestRemoveSkillFile_ConfinedToRoot(t *testing.T) {
	base := t.TempDir()
	skillsDir := filepath.Join(base, "skills")
	if err := os.MkdirAll(skillsDir, 0750); err != nil {
		t.Fatal(err)
	}

	// A sentinel file living OUTSIDE the skills dir that the traversal targets.
	sentinel := filepath.Join(base, "sentinel.md")
	if err := os.WriteFile(sentinel, []byte("precious"), 0600); err != nil {
		t.Fatal(err)
	}

	// "../sentinel" + ".md" would resolve to the sentinel if unconfined.
	err := configmcp.RemoveSkillFile(skillsDir, "../sentinel")
	if err == nil {
		t.Error("expected error: traversal removal should be rejected by os.Root")
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Errorf("traversal removal escaped the skills directory: sentinel gone: %v", statErr)
	}
}

// TestRemoveSkillFile_MissingIsNoError tolerates an absent file and an absent
// skills directory.
func TestRemoveSkillFile_MissingIsNoError(t *testing.T) {
	dir := t.TempDir()
	if err := configmcp.RemoveSkillFile(dir, "nonexistent"); err != nil {
		t.Errorf("removing a missing file should not error: %v", err)
	}
	if err := configmcp.RemoveSkillFile(filepath.Join(dir, "no-such-dir"), "x"); err != nil {
		t.Errorf("removing from a missing dir should not error: %v", err)
	}
}
