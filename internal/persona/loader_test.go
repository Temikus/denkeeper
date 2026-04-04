package persona

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoad_AllFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "I am the soul.")
	writeTestFile(t, filepath.Join(dir, "USER.md"), "User info here.")
	writeTestFile(t, filepath.Join(dir, "MEMORY.md"), "Current context.")

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
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Just soul.")

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
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "   \n  ")

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
	writeTestFile(t, f, "not a dir")

	_, err := Load(f)
	if err == nil {
		t.Fatal("expected error for non-directory path")
	}
}

func TestPersona_IsEditable_Defaults(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !p.IsEditable("soul") {
		t.Error("soul should be editable by user")
	}
	if !p.IsEditable("user") {
		t.Error("user should be editable by user")
	}
	if !p.IsEditable("memory") {
		t.Error("memory should be editable by user")
	}
	// Case-insensitive
	if !p.IsEditable("SOUL") {
		t.Error("IsEditable should be case-insensitive")
	}

	// Agent-mutability
	if p.IsAgentMutable("soul") {
		t.Error("soul should not be agent-mutable")
	}
	if !p.IsAgentMutable("user") {
		t.Error("user should be agent-mutable")
	}
	if !p.IsAgentMutable("memory") {
		t.Error("memory should be agent-mutable")
	}
	if p.IsAgentMutable("unknown") {
		t.Error("unknown section should not be agent-mutable")
	}
}

func TestPersona_IsEditable_UnknownSection(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

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
		Soul:   "Soul content",
		User:   "User content",
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

func TestLoad_SetsDir(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", p.Dir(), dir)
	}
}

func TestSave_Memory(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.Save("memory", "  New memory content.  "); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// In-memory state updated (trimmed).
	if p.Memory != "New memory content." {
		t.Errorf("Memory = %q, want %q", p.Memory, "New memory content.")
	}

	// File written to disk.
	data, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.TrimSpace(string(data)) != "New memory content." {
		t.Errorf("MEMORY.md = %q, want %q", strings.TrimSpace(string(data)), "New memory content.")
	}
}

func TestSave_User(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.Save("user", "Updated user info."); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if p.User != "Updated user info." {
		t.Errorf("User = %q, want %q", p.User, "Updated user info.")
	}
}

func TestSave_Soul(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Original soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.Save("soul", "New soul."); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if p.Soul != "New soul." {
		t.Errorf("Soul = %q, want %q", p.Soul, "New soul.")
	}

	data, err := os.ReadFile(filepath.Join(dir, "SOUL.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.TrimSpace(string(data)) != "New soul." {
		t.Errorf("SOUL.md = %q, want %q", strings.TrimSpace(string(data)), "New soul.")
	}
}

func TestSave_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.Save("MEMORY", "Via uppercase."); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if p.Memory != "Via uppercase." {
		t.Errorf("Memory = %q, want %q", p.Memory, "Via uppercase.")
	}
}

func TestSave_UnknownSection(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.Save("unknown", "content"); err == nil {
		t.Fatal("expected error for unknown section")
	}
}

func TestSave_NoDirSet(t *testing.T) {
	p := &Persona{Soul: "Soul."}

	if err := p.Save("memory", "content"); err == nil {
		t.Fatal("expected error when dir is not set")
	}
}

func TestUpdateMemory(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.UpdateMemory("Shorthand memory."); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}
	if p.Memory != "Shorthand memory." {
		t.Errorf("Memory = %q, want %q", p.Memory, "Shorthand memory.")
	}
}

func TestMemoryUpdateInstruction_WithDir(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	inst := p.MemoryUpdateInstruction()
	if inst == "" {
		t.Fatal("expected non-empty instruction when dir is set and memory is editable")
	}
	if !strings.Contains(inst, "[MEMORY_UPDATE]") {
		t.Error("instruction should contain the memory update tags")
	}
}

func TestMemoryUpdateInstruction_NoDir(t *testing.T) {
	p := &Persona{
		Soul: "Soul.",
		Editable: map[string]bool{
			"memory": true,
		},
	}

	if inst := p.MemoryUpdateInstruction(); inst != "" {
		t.Errorf("expected empty instruction when dir is not set, got %q", inst)
	}
}

func TestMemoryUpdateInstruction_MemoryNotEditable(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p.Editable["memory"] = false

	if inst := p.MemoryUpdateInstruction(); inst != "" {
		t.Errorf("expected empty instruction when memory is not editable, got %q", inst)
	}
}
