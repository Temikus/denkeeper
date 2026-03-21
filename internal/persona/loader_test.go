package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_AllFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("I am the soul."), 0644)
	os.WriteFile(filepath.Join(dir, "USER.md"), []byte("User info here."), 0644)
	os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("Current context."), 0644)

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Soul != "I am the soul." {
		t.Errorf("Soul = %q, want %q", p.Soul, "I am the soul.")
	}
	if p.User != "User info here." {
		t.Errorf("User = %q, want %q", p.User, "User info here.")
	}
	if p.Memory != "Current context." {
		t.Errorf("Memory = %q, want %q", p.Memory, "Current context.")
	}

	prompt := p.SystemPrompt()
	if !strings.Contains(prompt, "# Soul") {
		t.Error("SystemPrompt missing Soul header")
	}
	if !strings.Contains(prompt, "# User") {
		t.Error("SystemPrompt missing User header")
	}
	if !strings.Contains(prompt, "# Memory") {
		t.Error("SystemPrompt missing Memory header")
	}
}

func TestLoad_SoulOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Just soul."), 0644)

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Soul != "Just soul." {
		t.Errorf("Soul = %q, want %q", p.Soul, "Just soul.")
	}
	if p.User != "" {
		t.Errorf("User = %q, want empty", p.User)
	}
	if p.Memory != "" {
		t.Errorf("Memory = %q, want empty", p.Memory)
	}
}

func TestLoad_MissingSoul(t *testing.T) {
	dir := t.TempDir()

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for missing SOUL.md")
	}
}

func TestLoad_EmptySoul(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("   \n  "), 0644)

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for empty SOUL.md")
	}
}

func TestLoad_DirNotExist(t *testing.T) {
	_, err := Load("/nonexistent/path/to/persona")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestLoad_NotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	os.WriteFile(f, []byte("not a dir"), 0644)

	_, err := Load(f)
	if err == nil {
		t.Fatal("expected error for non-directory path")
	}
}

func TestPersona_IsEditable_Defaults(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Soul."), 0644)

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if p.IsEditable("soul") {
		t.Error("soul should not be editable")
	}
	if p.IsEditable("user") {
		t.Error("user should not be editable")
	}
	if !p.IsEditable("memory") {
		t.Error("memory should be editable")
	}
	// Case-insensitive
	if p.IsEditable("SOUL") {
		t.Error("IsEditable should be case-insensitive")
	}
}

func TestPersona_IsEditable_UnknownSection(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("Soul."), 0644)

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if p.IsEditable("unknown") {
		t.Error("unknown section should not be editable")
	}
}

func TestSystemPrompt_AllSections(t *testing.T) {
	p := &Persona{
		Soul:    "Soul content",
		User:    "User content",
		Memory: "Context content",
	}
	prompt := p.SystemPrompt()

	expected := "# Soul\n\nSoul content\n\n# User\n\nUser content\n\n# Memory\n\nContext content"
	if prompt != expected {
		t.Errorf("SystemPrompt =\n%s\nwant:\n%s", prompt, expected)
	}
}

func TestSystemPrompt_SoulOnly(t *testing.T) {
	p := &Persona{Soul: "Soul only"}
	prompt := p.SystemPrompt()

	if strings.Contains(prompt, "# User") {
		t.Error("SystemPrompt should not contain User header when User is empty")
	}
	if strings.Contains(prompt, "# Memory") {
		t.Error("SystemPrompt should not contain Memory header when Memory is empty")
	}
	if !strings.Contains(prompt, "Soul only") {
		t.Error("SystemPrompt should contain soul content")
	}
}
