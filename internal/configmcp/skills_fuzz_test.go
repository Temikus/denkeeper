package configmcp_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

// skillNameSeeds is the shared corpus of hostile and merely awkward skill
// names, used by every fuzz target over the confined skill-file helpers.
var skillNameSeeds = []string{
	"greet",
	"daily-github-trending",
	"",
	".",
	"..",
	"../escape",
	"../../etc/passwd",
	"a/b",
	`..\escape`,
	"....//x",
	"foo.md",
	"name with spaces",
	"UPPER_Case",
	"with\nnewline",
	"with\x00nul",
	strings.Repeat("a", 300),
	"スキル",
}

// FuzzApplySkillCreate_NeverEscapes asserts the safety invariant for arbitrary
// skill names: ApplySkillCreate either errors or writes strictly inside the
// skills directory — it must never create, modify, or delete anything outside
// it. A canary file living next to (but outside) the skills dir must survive
// every input untouched.
//
// Run deeper exploration with:  go test ./internal/configmcp -run=^$ -fuzz=FuzzApplySkillCreate_NeverEscapes
// In normal `go test` (CI) the seed corpus runs as ordinary subtests.
func FuzzApplySkillCreate_NeverEscapes(f *testing.F) {
	for _, s := range skillNameSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		base := t.TempDir()
		skillsDir := filepath.Join(base, "skills")

		// Canary file OUTSIDE the skills dir that a traversal would target.
		canary := filepath.Join(base, "canary")
		const canaryContent = "do-not-touch"
		if err := os.WriteFile(canary, []byte(canaryContent), 0600); err != nil {
			t.Fatal(err)
		}

		payload := configmcp.BuildSkillPayload(name, "desc", "1.0.0", nil, "body", 0, nil)
		// Error or success are both fine; the invariant below is what matters.
		_ = configmcp.ApplySkillCreate(skillsDir, func(skill.Skill) {}, ioTestLogger(), payload, 0)

		// Invariant 1: no regular file exists anywhere under base except the
		// canary and files inside skillsDir.
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // skip unreadable entries; we only assert on files we can see
			}
			if path == canary {
				return nil
			}
			rel, relErr := filepath.Rel(skillsDir, path)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("file created outside skills dir for name %q: %s", name, path)
			}
			return nil
		})

		// Invariant 2: the canary is intact.
		got, err := os.ReadFile(canary)
		if err != nil {
			t.Errorf("canary removed/unreadable for name %q: %v", name, err)
		} else if string(got) != canaryContent {
			t.Errorf("canary modified for name %q: got %q", name, string(got))
		}
	})
}

// FuzzReadSkillFile_NeverEscapes is the read-side twin: for arbitrary names,
// ReadSkillFile either errors or returns content from strictly inside the
// skills directory. It must never hand back a byte of the secret living beside
// it — which matters more than usual here, because the undo journal persists
// whatever this returns.
//
// Run deeper exploration with:  go test ./internal/configmcp -run=^$ -fuzz=FuzzReadSkillFile_NeverEscapes
func FuzzReadSkillFile_NeverEscapes(f *testing.F) {
	for _, s := range skillNameSeeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		base := t.TempDir()
		skillsDir := filepath.Join(base, "skills")
		if err := os.MkdirAll(skillsDir, 0750); err != nil {
			t.Fatal(err)
		}

		// A secret OUTSIDE the skills dir, plus a symlink INSIDE it pointing at
		// the secret — so a name that dodges validation still has to be stopped
		// by os.Root rather than by the name check alone.
		const secretContent = "do-not-read"
		secret := filepath.Join(base, "secret.md")
		if err := os.WriteFile(secret, []byte(secretContent), 0600); err != nil {
			t.Fatal(err)
		}
		_ = os.Symlink(secret, filepath.Join(skillsDir, "link.md"))

		// One legitimate skill, so the happy path is exercised too.
		payload := configmcp.BuildSkillPayload("greet", "desc", "1.0.0", nil, "body", 0, nil)
		if err := configmcp.ApplySkillCreate(skillsDir, func(skill.Skill) {}, ioTestLogger(), payload, 0); err != nil {
			t.Fatal(err)
		}

		content, ok, err := configmcp.ReadSkillFile(skillsDir, name)
		if strings.Contains(content, secretContent) {
			t.Errorf("name %q leaked content from outside the skills dir", name)
		}
		if err != nil && ok {
			t.Errorf("name %q returned ok with an error: %v", name, err)
		}
		if !ok && content != "" {
			t.Errorf("name %q returned content while reporting not-found: %q", name, content)
		}
	})
}
