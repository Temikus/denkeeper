package configmcp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

// TestReadSkillFile_ReturnsRawBytes is the happy path: the reader hands back
// exactly what is on disk, trailer included. The undo journal stores these
// bytes verbatim, so any normalization here would corrupt a restore.
func TestReadSkillFile_ReturnsRawBytes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	payload := configmcp.BuildSkillPayload("greet", "desc", "1.0.0", nil, "hello body", 0, nil)
	if err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {}, ioTestLogger(), payload, 0); err != nil {
		t.Fatalf("seed create: %v", err)
	}

	got, ok, err := configmcp.ReadSkillFile(dir, "greet")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok {
		t.Fatal("expected the skill file to be found")
	}

	onDisk, err := os.ReadFile(filepath.Join(dir, "greet.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(onDisk) {
		t.Errorf("read bytes differ from the file:\n got %q\nwant %q", got, string(onDisk))
	}
}

// TestReadSkillFile_PayloadRoundTrip proves the read/write pair is stable:
// writing back what was read reproduces the file byte for byte, which is what
// makes a revert byte-identical rather than newline-creeping.
func TestReadSkillFile_PayloadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	payload := configmcp.BuildSkillPayload("greet", "desc", "1.0.0", nil, "hello body", 0, nil)
	if err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {}, ioTestLogger(), payload, 0); err != nil {
		t.Fatalf("seed create: %v", err)
	}

	first, _, err := configmcp.ReadSkillFile(dir, "greet")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := configmcp.ApplySkillUpdate(dir, func(string, skill.Skill) bool { return true },
		ioTestLogger(), "greet", configmcp.SkillPayloadFromFile(first), 0); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	second, _, err := configmcp.ReadSkillFile(dir, "greet")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if second != first {
		t.Errorf("round trip changed the file:\n got %q\nwant %q", second, first)
	}
}

// TestReadSkillFile_MissingIsNotAnError distinguishes "no such skill" from a
// read failure: the journal treats the former as "nothing was here" and the
// latter as a reason to abort.
func TestReadSkillFile_MissingIsNotAnError(t *testing.T) {
	dir := t.TempDir()

	if _, ok, err := configmcp.ReadSkillFile(dir, "nonexistent"); err != nil || ok {
		t.Errorf("missing file: got (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if _, ok, err := configmcp.ReadSkillFile(filepath.Join(dir, "no-such-dir"), "greet"); err != nil || ok {
		t.Errorf("missing dir: got (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

// TestReadSkillFile_ConfinedToRoot proves a traversal name cannot read a file
// outside the skills directory.
func TestReadSkillFile_ConfinedToRoot(t *testing.T) {
	base := t.TempDir()
	skillsDir := filepath.Join(base, "skills")
	if err := os.MkdirAll(skillsDir, 0750); err != nil {
		t.Fatal(err)
	}

	// A secret living OUTSIDE the skills dir that the traversal targets.
	secret := filepath.Join(base, "sentinel.md")
	if err := os.WriteFile(secret, []byte("precious"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../sentinel", "../../sentinel", "/etc/passwd"} {
		content, ok, err := configmcp.ReadSkillFile(skillsDir, name)
		if err == nil {
			t.Errorf("ReadSkillFile(%q) succeeded; it must be rejected", name)
		}
		if ok || strings.Contains(content, "precious") {
			t.Errorf("ReadSkillFile(%q) leaked content from outside the skills dir", name)
		}
	}
}

// TestReadSkillFile_SymlinkEscapeRefused is the os.Root proof that name
// validation cannot give: a symlink inside the skills directory with a
// perfectly legal name, pointing at a file outside it.
func TestReadSkillFile_SymlinkEscapeRefused(t *testing.T) {
	base := t.TempDir()
	skillsDir := filepath.Join(base, "skills")
	if err := os.MkdirAll(skillsDir, 0750); err != nil {
		t.Fatal(err)
	}

	secret := filepath.Join(base, "secret.md")
	if err := os.WriteFile(secret, []byte("precious"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(skillsDir, "escape.md")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	content, ok, err := configmcp.ReadSkillFile(skillsDir, "escape")
	if err == nil {
		t.Error("expected an error: os.Root must refuse a symlink leaving the root")
	}
	if ok || strings.Contains(content, "precious") {
		t.Errorf("symlink read escaped the skills directory: ok=%v content=%q", ok, content)
	}
}

// TestReadSkillFile_SymlinkedSkillsDirIsAllowed confirms confinement is about
// escaping the directory, not about how the directory itself is reached: a
// skills dir that IS a symlink still works.
func TestReadSkillFile_SymlinkedSkillsDirIsAllowed(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real-skills")
	if err := os.MkdirAll(realDir, 0750); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(base, "skills")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	payload := configmcp.BuildSkillPayload("greet", "", "1.0.0", nil, "body", 0, nil)
	if err := configmcp.ApplySkillCreate(linkDir, func(skill.Skill) {}, ioTestLogger(), payload, 0); err != nil {
		t.Fatalf("create through symlinked dir: %v", err)
	}

	got, ok, err := configmcp.ReadSkillFile(linkDir, "greet")
	if err != nil || !ok {
		t.Fatalf("read through symlinked dir: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(got, "body") {
		t.Errorf("unexpected content: %q", got)
	}
}
