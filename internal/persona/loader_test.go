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
	if p.GetSoul() != "I am the soul." {
		t.Errorf("Soul = %q, want %q", p.GetSoul(), "I am the soul.")
	}
	if p.GetUser() != "User info here." {
		t.Errorf("User = %q, want %q", p.GetUser(), "User info here.")
	}
	if p.GetMemory() != "Current context." {
		t.Errorf("Memory = %q, want %q", p.GetMemory(), "Current context.")
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
	if p.GetSoul() != "Just soul." {
		t.Errorf("Soul = %q, want %q", p.GetSoul(), "Just soul.")
	}
	if p.GetUser() != "" {
		t.Errorf("User = %q, want empty", p.GetUser())
	}
	if p.GetMemory() != "" {
		t.Errorf("Memory = %q, want empty", p.GetMemory())
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
	if !p.IsAgentMutable("soul") {
		t.Error("soul should be agent-mutable")
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
		soul:   "Soul content",
		user:   "User content",
		memory: "Context content",
	}
	prompt := p.SystemPrompt()

	expected := "# Soul\n\nSoul content\n\n# User\n\nUser content\n\n# Memory\n\nContext content"
	if prompt != expected {
		t.Errorf("SystemPrompt =\n%s\nwant:\n%s", prompt, expected)
	}
}

func TestSystemPrompt_SoulOnly(t *testing.T) {
	p := &Persona{soul: "Soul only"}
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
	if p.GetMemory() != "New memory content." {
		t.Errorf("Memory = %q, want %q", p.GetMemory(), "New memory content.")
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
	if p.GetUser() != "Updated user info." {
		t.Errorf("User = %q, want %q", p.GetUser(), "Updated user info.")
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
	if p.GetSoul() != "New soul." {
		t.Errorf("Soul = %q, want %q", p.GetSoul(), "New soul.")
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
	if p.GetMemory() != "Via uppercase." {
		t.Errorf("Memory = %q, want %q", p.GetMemory(), "Via uppercase.")
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
	p := &Persona{soul: "Soul."}

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
	if p.GetMemory() != "Shorthand memory." {
		t.Errorf("Memory = %q, want %q", p.GetMemory(), "Shorthand memory.")
	}
}

func TestAppendMemoryEntry_Empty(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.AppendMemoryEntry("First entry"); err != nil {
		t.Fatalf("AppendMemoryEntry: %v", err)
	}
	if p.GetMemory() != "First entry" {
		t.Errorf("Memory = %q, want %q", p.GetMemory(), "First entry")
	}
}

func TestAppendMemoryEntry_Existing(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")
	writeTestFile(t, filepath.Join(dir, "MEMORY.md"), "## Existing\n\nSome context.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.AppendMemoryEntry("## New\n\nNew context."); err != nil {
		t.Fatalf("AppendMemoryEntry: %v", err)
	}
	mem := p.GetMemory()
	if !strings.Contains(mem, "Existing") || !strings.Contains(mem, "New context.") {
		t.Errorf("Memory should contain both entries, got %q", mem)
	}
	if !strings.Contains(mem, "---") {
		t.Error("Memory entries should be separated by ---")
	}
}

func TestAppendMemoryEntry_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.AppendMemoryEntry("  "); err == nil {
		t.Error("expected error for empty entry")
	}
}

func TestRemoveMemoryEntry_Found(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")
	writeTestFile(t, filepath.Join(dir, "MEMORY.md"), "## Keep\n\nKeep this.\n\n---\n\n## Remove\n\nRemove this.\n\n---\n\n## Also Keep\n\nKeep too.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.RemoveMemoryEntry("Remove"); err != nil {
		t.Fatalf("RemoveMemoryEntry: %v", err)
	}
	mem := p.GetMemory()
	if strings.Contains(mem, "Remove this.") {
		t.Errorf("Memory should not contain removed entry, got %q", mem)
	}
	if !strings.Contains(mem, "Keep this.") || !strings.Contains(mem, "Keep too.") {
		t.Errorf("Memory should still contain kept entries, got %q", mem)
	}
}

func TestRemoveMemoryEntry_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")
	writeTestFile(t, filepath.Join(dir, "MEMORY.md"), "## Existing\n\nData.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.RemoveMemoryEntry("NonExistent"); err == nil {
		t.Error("expected error when heading not found")
	}
}

func TestRemoveMemoryEntry_EmptyMemory(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := p.RemoveMemoryEntry("Anything"); err == nil {
		t.Error("expected error when memory is empty")
	}
}

// --- IDENTITY.md tests ---

func TestParseIdentity_Full(t *testing.T) {
	content := "---\nname: Moltis\nemoji: \"🧊\"\ntheme: thorough and methodical\n---\n\nAdditional identity notes."
	id, err := parseIdentity(content)
	if err != nil {
		t.Fatalf("parseIdentity: %v", err)
	}
	if id.Name != "Moltis" {
		t.Errorf("Name = %q, want %q", id.Name, "Moltis")
	}
	if id.Emoji != "🧊" {
		t.Errorf("Emoji = %q, want %q", id.Emoji, "🧊")
	}
	if id.Theme != "thorough and methodical" {
		t.Errorf("Theme = %q, want %q", id.Theme, "thorough and methodical")
	}
	if id.Body != "Additional identity notes." {
		t.Errorf("Body = %q, want %q", id.Body, "Additional identity notes.")
	}
}

func TestParseIdentity_Partial(t *testing.T) {
	content := "---\nname: Helper\n---"
	id, err := parseIdentity(content)
	if err != nil {
		t.Fatalf("parseIdentity: %v", err)
	}
	if id.Name != "Helper" {
		t.Errorf("Name = %q, want %q", id.Name, "Helper")
	}
	if id.Emoji != "" {
		t.Errorf("Emoji = %q, want empty", id.Emoji)
	}
	if id.Theme != "" {
		t.Errorf("Theme = %q, want empty", id.Theme)
	}
	if id.Body != "" {
		t.Errorf("Body = %q, want empty", id.Body)
	}
}

func TestParseIdentity_NoFrontmatter(t *testing.T) {
	content := "Just plain markdown identity notes."
	id, err := parseIdentity(content)
	if err != nil {
		t.Fatalf("parseIdentity: %v", err)
	}
	if id.Name != "" {
		t.Errorf("Name = %q, want empty", id.Name)
	}
	if id.Body != "Just plain markdown identity notes." {
		t.Errorf("Body = %q, want %q", id.Body, "Just plain markdown identity notes.")
	}
}

func TestParseIdentity_Empty(t *testing.T) {
	id, err := parseIdentity("")
	if err != nil {
		t.Fatalf("parseIdentity: %v", err)
	}
	if id.Name != "" || id.Emoji != "" || id.Theme != "" || id.Body != "" {
		t.Error("expected all fields empty for empty input")
	}
}

func TestLoad_WithIdentity(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "I am the soul.")
	writeTestFile(t, filepath.Join(dir, "IDENTITY.md"), "---\nname: TestBot\nemoji: \"🤖\"\ntheme: helpful\n---\n\nExtra notes.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.GetIdentity() == nil {
		t.Fatal("Identity should not be nil when IDENTITY.md is present")
	}
	if p.GetIdentity().Name != "TestBot" {
		t.Errorf("Identity.Name = %q, want %q", p.GetIdentity().Name, "TestBot")
	}
	if p.GetIdentity().Emoji != "🤖" {
		t.Errorf("Identity.Emoji = %q, want %q", p.GetIdentity().Emoji, "🤖")
	}
	if p.GetIdentity().Theme != "helpful" {
		t.Errorf("Identity.Theme = %q, want %q", p.GetIdentity().Theme, "helpful")
	}
	if p.GetIdentity().Body != "Extra notes." {
		t.Errorf("Identity.Body = %q, want %q", p.GetIdentity().Body, "Extra notes.")
	}
	if p.GetIdentityRaw() == "" {
		t.Error("IdentityRaw should be set")
	}
	if !p.IsEditable("identity") {
		t.Error("identity should be editable")
	}
	if !p.IsAgentMutable("identity") {
		t.Error("identity should be agent-mutable")
	}
}

func TestLoad_WithoutIdentity(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Just soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.GetIdentity() != nil {
		t.Error("Identity should be nil when IDENTITY.md is absent")
	}
	if p.GetIdentityRaw() != "" {
		t.Errorf("IdentityRaw = %q, want empty", p.GetIdentityRaw())
	}
}

func TestSystemPrompt_WithIdentity(t *testing.T) {
	p := &Persona{
		soul: "Soul content",
		identity: &Identity{
			Name:  "TestBot",
			Emoji: "🤖",
			Theme: "helpful and concise",
			Body:  "Some extra identity notes.",
		},
	}
	prompt := p.SystemPrompt()

	if !strings.Contains(prompt, "# Identity") {
		t.Error("SystemPrompt should contain Identity header")
	}
	if !strings.Contains(prompt, "Your name is TestBot 🤖.") {
		t.Errorf("SystemPrompt should contain name line, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Your vibe: helpful and concise.") {
		t.Error("SystemPrompt should contain theme line")
	}
	if !strings.Contains(prompt, "Some extra identity notes.") {
		t.Error("SystemPrompt should contain body")
	}
	// Identity should come before Soul.
	idIdx := strings.Index(prompt, "# Identity")
	soulIdx := strings.Index(prompt, "# Soul")
	if idIdx >= soulIdx {
		t.Error("Identity section should appear before Soul section")
	}
}

func TestSystemPrompt_IdentityPartialFields(t *testing.T) {
	p := &Persona{
		soul:     "Soul content",
		identity: &Identity{Name: "Helper"},
	}
	prompt := p.SystemPrompt()

	if !strings.Contains(prompt, "Your name is Helper.") {
		t.Error("SystemPrompt should contain name without emoji")
	}
	if strings.Contains(prompt, "Your vibe:") {
		t.Error("SystemPrompt should not contain theme line when theme is empty")
	}
}

func TestSystemPrompt_WithoutIdentity_BackwardCompat(t *testing.T) {
	p := &Persona{
		soul:   "Soul content",
		user:   "User content",
		memory: "Context content",
	}
	prompt := p.SystemPrompt()

	expected := "# Soul\n\nSoul content\n\n# User\n\nUser content\n\n# Memory\n\nContext content"
	if prompt != expected {
		t.Errorf("SystemPrompt =\n%s\nwant:\n%s", prompt, expected)
	}
}

func TestSave_Identity(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SOUL.md"), "Soul.")

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	newContent := "---\nname: Updated\nemoji: \"✨\"\ntheme: sparkly\n---\n\nNew body."
	if err := p.Save("identity", newContent); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if p.GetIdentityRaw() != strings.TrimSpace(newContent) {
		t.Errorf("IdentityRaw = %q, want %q", p.GetIdentityRaw(), strings.TrimSpace(newContent))
	}
	if p.GetIdentity() == nil {
		t.Fatal("Identity should not be nil after save")
	}
	if p.GetIdentity().Name != "Updated" {
		t.Errorf("Identity.Name = %q, want %q", p.GetIdentity().Name, "Updated")
	}
	if p.GetIdentity().Emoji != "✨" {
		t.Errorf("Identity.Emoji = %q, want %q", p.GetIdentity().Emoji, "✨")
	}

	// Verify file on disk.
	data, err := os.ReadFile(filepath.Join(dir, "IDENTITY.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "name: Updated") {
		t.Error("IDENTITY.md should contain updated name on disk")
	}
}

func TestNewEmpty_IncludesIdentity(t *testing.T) {
	p := NewEmpty("/tmp/test")
	if !p.IsEditable("identity") {
		t.Error("NewEmpty persona should have identity as editable")
	}
}
