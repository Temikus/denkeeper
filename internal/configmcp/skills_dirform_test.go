package configmcp_test

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

// seedDirSkill writes <skillsDir>/<folder>/SKILL.md (the kelos YAML skill)
// with a reference file beside it, and loads the directory the way startup
// does, so the skill carries its Dir.
func seedDirSkill(t *testing.T, skillsDir, folder string) []skill.Skill {
	t.Helper()
	refs := filepath.Join(skillsDir, folder, "references")
	if err := os.MkdirAll(refs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, folder, "SKILL.md"), []byte(yamlSkillFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refs, "notes.md"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	skills, err := skill.LoadDir(skillsDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(skills) != 1 || skills[0].Dir == "" {
		t.Fatalf("seeded skills = %+v, want one directory skill", skills)
	}
	return skills
}

func dirSkillServer(t *testing.T, folder string) (func(string, map[string]any) (string, bool), string, map[string]skill.Skill) {
	t.Helper()
	return memSkillServer(t, func(skillsDir string) []skill.Skill { return seedDirSkill(t, skillsDir, folder) })
}

// assertDirSkillFile checks the edit landed in <dir>/kelos/SKILL.md, that no
// flat kelos.md was created beside it, and that the reference file survived.
func assertDirSkillFile(t *testing.T, dir, wantInFile string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "kelos", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), wantInFile) {
		t.Errorf("kelos/SKILL.md =\n%s\nwant it to contain %q", data, wantInFile)
	}
	if _, err := os.Stat(filepath.Join(dir, "kelos.md")); !os.IsNotExist(err) {
		t.Errorf("a flat kelos.md was written beside the folder: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "kelos", "references", "notes.md")); err != nil || string(got) != "keep me" {
		t.Errorf("reference file = %q, %v", got, err)
	}
}

func TestSkillPatch_DirectorySkillWritesItsSKILLmd(t *testing.T) {
	call, dir, _ := dirSkillServer(t, "kelos")
	if text, isErr := call("skill_patch", map[string]any{
		"name": "kelos", "old_string": "Old body.", "new_string": "Patched body.",
	}); isErr {
		t.Fatalf("skill_patch: %s", text)
	}
	assertDirSkillFile(t, dir, "Patched body.")
}

func TestSkillUpdate_DirectorySkillWritesItsSKILLmd(t *testing.T) {
	call, dir, mem := dirSkillServer(t, "kelos")
	if text, isErr := call("skill_update", map[string]any{"name": "kelos", "description": "Changed."}); isErr {
		t.Fatalf("skill_update: %s", text)
	}
	assertDirSkillFile(t, dir, "description: Changed.")
	if mem["kelos"].Description != "Changed." {
		t.Errorf("memory description = %q", mem["kelos"].Description)
	}
}

// A rename would have to move the folder and its sub-files, which the undo
// journal cannot reverse, so it is refused before anything is written.
func TestSkillUpdate_DirectorySkillRenameIsRefused(t *testing.T) {
	call, dir, mem := dirSkillServer(t, "kelos")
	text, isErr := call("skill_update", map[string]any{"name": "kelos", "new_name": "kelos-two"})
	if !isErr || !strings.Contains(text, "rename its folder by hand, then change the name field in its SKILL.md (the skill needs a manual edit)") {
		t.Fatalf("rename = %q (error %v), want a refusal", text, isErr)
	}
	for _, p := range []string{"kelos-two.md", "kelos-two"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s exists after a refused rename", p)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "kelos", "SKILL.md")); string(data) != yamlSkillFile {
		t.Errorf("SKILL.md changed after a refused rename:\n%s", data)
	}
	if _, ok := mem["kelos"]; !ok {
		t.Error("kelos left memory after a refused rename")
	}
}

// A folder not named after the skill cannot be found by name, so an edit
// would write a second, flat copy. It is refused instead.
func TestSkillUpdate_DirectorySkillInOtherFolderIsRefused(t *testing.T) {
	call, dir, _ := dirSkillServer(t, "kelos-folder")
	text, isErr := call("skill_update", map[string]any{"name": "kelos", "description": "Changed."})
	if !isErr || !strings.Contains(text, "Edit kelos-folder/SKILL.md in the skills directory (the skill needs a manual edit)") || strings.Contains(text, dir) {
		t.Fatalf("update = %q (error %v), want a refusal naming the folder relatively", text, isErr)
	}
	if _, err := os.Stat(filepath.Join(dir, "kelos.md")); !os.IsNotExist(err) {
		t.Errorf("a flat kelos.md was written: %v", err)
	}
}

func TestReadSkillFile_ReadsADirectorySkill(t *testing.T) {
	dir := t.TempDir()
	seedDirSkill(t, dir, "kelos")
	got, ok, err := configmcp.ReadSkillFile(dir, "kelos")
	if err != nil || !ok || got != yamlSkillFile {
		t.Fatalf("ReadSkillFile = %q, %v, %v; want the SKILL.md bytes", got, ok, err)
	}
}

// After a directory skill is deleted its folder stays, so a create of the
// same name is refused rather than inheriting the old skill's files.
func TestApplySkillCreate_RefusesALeftoverFolder(t *testing.T) {
	dir := t.TempDir()
	seedDirSkill(t, dir, "kelos")
	if err := configmcp.RemoveSkillFile(dir, "kelos"); err != nil {
		t.Fatal(err)
	}
	if exists, err := configmcp.SkillFileExists(dir, "kelos"); err != nil || !exists {
		t.Errorf("SkillFileExists = %v, %v; want the leftover folder to count", exists, err)
	}

	payload := configmcp.BuildSkillPayload("kelos", "New.", "1", nil, "body", 0, 0, nil)
	err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {}, ioTestLogger(), payload, 0)
	if !errors.Is(err, configmcp.ErrSkillExists) || !strings.Contains(err.Error(), "folder with no SKILL.md") {
		t.Fatalf("create = %v, want a refusal naming the leftover folder", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kelos", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("SKILL.md was written into the leftover folder: %v", err)
	}
}

// After an edit, memory still knows the skill lives in a folder, so its
// sub-files stay readable.
func TestSkillPatch_DirectorySkillKeepsItsSubFiles(t *testing.T) {
	call, _, mem := dirSkillServer(t, "kelos")
	if text, isErr := call("skill_patch", map[string]any{
		"name": "kelos", "old_string": "Old body.", "new_string": "Patched body.",
	}); isErr {
		t.Fatalf("skill_patch: %s", text)
	}
	if sk := mem["kelos"]; sk.Dir == "" || len(sk.SubFileNames) != 1 {
		t.Errorf("after patch: Dir %q, sub-files %v", sk.Dir, sk.SubFileNames)
	}
	text, isErr := call("skill_read_file", map[string]any{"skill": "kelos", "file_path": "references/notes.md"})
	if isErr || !strings.Contains(text, "keep me") {
		t.Errorf("skill_read_file after patch = %q (error %v)", text, isErr)
	}
}

// A rename must not land on a name that is already taken on disk, loaded or
// not: an unloaded flat file would be overwritten, a folder written into.
func TestApplySkillRename_RefusesATakenDestination(t *testing.T) {
	for _, taken := range []string{"other.md", "other"} {
		dir := t.TempDir()
		writeSkill(t, dir, "kelos", yamlSkillFile)
		target := filepath.Join(dir, taken)
		if taken == "other" {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(target, []byte("not loaded"), 0o600); err != nil {
			t.Fatal(err)
		}
		payload := strings.Replace(yamlSkillFile, "name: kelos", "name: other", 1)
		err := configmcp.ApplySkillRename(dir, func(string) bool { return true }, func(skill.Skill) {}, ioTestLogger(), "kelos", payload, 0)
		if !errors.Is(err, configmcp.ErrSkillExists) {
			t.Errorf("rename onto %s = %v, want ErrSkillExists", taken, err)
		}
		if data, _ := os.ReadFile(filepath.Join(dir, "kelos.md")); string(data) != yamlSkillFile {
			t.Errorf("rename onto %s changed kelos.md", taken)
		}
		if taken == "other.md" {
			if data, _ := os.ReadFile(target); string(data) != "not loaded" {
				t.Errorf("unloaded destination file overwritten: %q", data)
			}
		} else if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
			t.Errorf("SKILL.md written into the destination folder: %v", err)
		}
	}
}

// Only a folder is a leftover: a plain file called <name> does not block a
// create.
func TestApplySkillCreate_ExtensionlessFileIsNotALeftoverFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kelos"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exists, err := configmcp.SkillFileExists(dir, "kelos"); err != nil || exists {
		t.Errorf("SkillFileExists = %v, %v; want false for a plain file", exists, err)
	}
	payload := configmcp.BuildSkillPayload("kelos", "d", "1", nil, "body", 0, 0, nil)
	if err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {}, ioTestLogger(), payload, 0); err != nil {
		t.Errorf("create beside a plain file = %v", err)
	}
}

func TestRemoveSkillFile_RemovesADirectorySkillsSKILLmd(t *testing.T) {
	dir := t.TempDir()
	seedDirSkill(t, dir, "kelos")
	if err := configmcp.RemoveSkillFile(dir, "kelos"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kelos", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("SKILL.md still there: %v", err)
	}
}
