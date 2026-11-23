package configmcp_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

// yamlSkillFile is an Agent Skills style file: YAML frontmatter with keys
// Denkeeper does not read (license, metadata) and a folded scalar.
const yamlSkillFile = "---\n" +
	"name: kelos\n" +
	"description: >-\n" +
	"  Folded description\n" +
	"  over two lines.\n" +
	"license: MIT\n" +
	"metadata:\n" +
	"  author: someone\n" +
	"  tags: [a, b]\n" +
	"---\n\n" +
	"# Kelos\n\nOld body.\n"

// mustMerge calls MergeSkillFields and fails the test on an error.
func mustMerge(t *testing.T, name string, existing skill.Skill, desc, ver *string, triggers []string, body *string, rounds, outTokens *int, requires []string) string {
	t.Helper()
	payload, err := configmcp.MergeSkillFields(name, existing, desc, ver, triggers, body, rounds, outTokens, requires)
	if err != nil {
		t.Fatalf("MergeSkillFields: %v", err)
	}
	return payload
}

// writeSkill writes content as <dir>/<name>.md and parses it back from disk.
func writeSkill(t *testing.T, dir, name, content string) skill.Skill {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return readSkill(t, path)
}

func readSkill(t *testing.T, path string) skill.Skill {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := skill.ParseFile(path, data)
	if err != nil {
		t.Fatalf("ParseFile %s: %v", path, err)
	}
	return *s
}

func TestMergeSkillFields_YAMLBodyOnlyKeepsBlockBytes(t *testing.T) {
	existing := writeSkill(t, t.TempDir(), "kelos", yamlSkillFile)
	body := "New body."
	payload := mustMerge(t, "kelos", existing, nil, nil, nil, &body, nil, nil, nil)

	want := "---\n" + existing.Frontmatter + "\n---\n\nNew body."
	if payload != want {
		t.Errorf("payload =\n%s\nwant\n%s", payload, want)
	}
}

func TestMergeSkillFields_YAMLDescriptionKeepsDialectAndUnknownKeys(t *testing.T) {
	existing := writeSkill(t, t.TempDir(), "kelos", yamlSkillFile)
	desc := "New description"
	payload := mustMerge(t, "kelos", existing, &desc, nil, nil, nil, nil, nil, nil)

	if !strings.HasPrefix(payload, "---\n") {
		t.Fatalf("dialect changed:\n%s", payload)
	}
	for _, kept := range []string{"license: MIT", "metadata:", "author: someone", "tags: [a, b]"} {
		if !strings.Contains(payload, kept) {
			t.Errorf("payload lost %q:\n%s", kept, payload)
		}
	}
	got, err := skill.ParseFile("(merged)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got.Description != desc || got.Dialect != skill.DialectYAML {
		t.Errorf("description/dialect = %q/%q", got.Description, got.Dialect)
	}
}

func TestMergeSkillFields_TOMLUnknownKeyKeptOnVersionChange(t *testing.T) {
	existing := writeSkill(t, t.TempDir(), "tidy",
		"+++\nname = \"tidy\"\nversion = \"1.0\"\nlicense = \"MIT\"\n+++\n\nBody.\n")
	ver := "2.0"
	payload := mustMerge(t, "tidy", existing, nil, &ver, nil, nil, nil, nil, nil)

	if !strings.HasPrefix(payload, "+++\n") {
		t.Fatalf("dialect changed:\n%s", payload)
	}
	got, err := skill.ParseFile("(merged)", []byte(payload))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if got.Version != "2.0" || !strings.Contains(got.Frontmatter, "license") || !strings.Contains(got.Frontmatter, "MIT") {
		t.Errorf("version = %q, frontmatter =\n%s", got.Version, got.Frontmatter)
	}
}

// yamlSkillServer serves one YAML skill from a real file, refreshing memory
// from what each write parses to, as the engine does.
func yamlSkillServer(t *testing.T) (call func(string, map[string]any) (string, bool), dir string, mem map[string]skill.Skill) {
	t.Helper()
	var mu sync.RWMutex
	mem = map[string]skill.Skill{}
	session, deps := newTestServer(t, func(d *configmcp.Deps) {
		mem["kelos"] = writeSkill(t, d.AgentSkillsDir, "kelos", yamlSkillFile)
		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			s, ok := mem[name]
			return s, ok
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			if _, ok := mem[name]; !ok {
				return false
			}
			mem[name] = s
			return true
		}
		d.RemoveSkill = func(name string) bool {
			mu.Lock()
			defer mu.Unlock()
			_, ok := mem[name]
			delete(mem, name)
			return ok
		}
		d.AppendSkill = func(s skill.Skill) {
			mu.Lock()
			defer mu.Unlock()
			mem[s.Name] = s
		}
	})
	call = func(tool string, args map[string]any) (string, bool) { return callTool(t, session, tool, args) }
	return call, deps.AgentSkillsDir, mem
}

func TestSkillPatch_YAMLKeepsFrontmatterBytes(t *testing.T) {
	call, dir, mem := yamlSkillServer(t)
	before := mem["kelos"].Frontmatter

	if text, isErr := call("skill_patch", map[string]any{
		"name": "kelos", "old_string": "Old body.", "new_string": "Patched body.",
	}); isErr {
		t.Fatalf("skill_patch: %s", text)
	}

	data, err := os.ReadFile(filepath.Join(dir, "kelos.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\n" + before + "\n---\n\n# Kelos\n\nPatched body.\n"
	if string(data) != want {
		t.Errorf("file =\n%q\nwant\n%q", data, want)
	}
}

func TestSkillUpdate_RenameKeepsYAML(t *testing.T) {
	call, dir, _ := yamlSkillServer(t)

	if text, isErr := call("skill_update", map[string]any{"name": "kelos", "new_name": "kelos-two"}); isErr {
		t.Fatalf("skill_update rename: %s", text)
	}

	if _, err := os.Stat(filepath.Join(dir, "kelos.md")); !os.IsNotExist(err) {
		t.Errorf("old file still there: %v", err)
	}
	got := readSkill(t, filepath.Join(dir, "kelos-two.md"))
	if got.Dialect != skill.DialectYAML || got.Name != "kelos-two" || !strings.Contains(got.Frontmatter, "license: MIT") {
		t.Errorf("renamed skill = dialect %q, name %q, frontmatter:\n%s", got.Dialect, got.Name, got.Frontmatter)
	}
}

func TestApplySkillCreate_YAMLTraversalNameRefused(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "skills")
	payload := "---\nname: ../x\ndescription: d\n---\n\nbody"

	if err := configmcp.ApplySkillCreate(dir, func(skill.Skill) {}, ioTestLogger(), payload, 0); err == nil {
		t.Fatal("expected a refusal for a traversal name")
	}
	if _, err := os.Stat(filepath.Join(base, "x.md")); !os.IsNotExist(err) {
		t.Errorf("file written outside the skills dir: %v", err)
	}
}

func TestApplySkillUpdate_YAMLOverSizeCapRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	writeSkill(t, dir, "kelos", yamlSkillFile)
	payload := "---\nname: kelos\n---\n\n" + strings.Repeat("x", 500)

	err := configmcp.ApplySkillUpdate(dir, func(string, skill.Skill) bool { return true }, ioTestLogger(), "kelos", payload, 100)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want the size cap", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "kelos.md")); string(data) != yamlSkillFile {
		t.Error("file changed despite the refusal")
	}
}
