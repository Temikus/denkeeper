package configmcp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteSkillFileAtomic_RootBackstop proves the os.Root boundary alone
// confines a traversal name — bypassing ValidateSkillName by calling the
// unexported write helper directly. This is the kernel/runtime backstop that
// guarantees safety even if a name ever slipped past the denylist.
func TestWriteSkillFileAtomic_RootBackstop(t *testing.T) {
	base := t.TempDir()
	skillsDir := filepath.Join(base, "skills")
	root, err := openSkillRoot(skillsDir)
	if err != nil {
		t.Fatalf("openSkillRoot: %v", err)
	}
	defer func() { _ = root.Close() }()

	// Names that genuinely resolve outside root on the test platform. (On Unix
	// a backslash is a valid filename char, so `..\escape` would NOT escape —
	// that case is handled by ValidateSkillName, not os.Root.)
	for _, name := range []string{"../escape", "../../escape", "../../../etc/passwd"} {
		if err := writeSkillFileAtomic(root, name, "payload"); err == nil {
			t.Errorf("writeSkillFileAtomic(%q) succeeded; os.Root must reject it", name)
		}
	}

	// Nothing leaked above the skills dir.
	for _, name := range []string{"escape.md", "escape.md.tmp"} {
		if _, statErr := os.Stat(filepath.Join(base, name)); !os.IsNotExist(statErr) {
			t.Errorf("file escaped the skills directory: %s exists", name)
		}
	}
}
