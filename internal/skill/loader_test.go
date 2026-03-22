package skill

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---------------------------------------------------------------------------
// ParseFile tests
// ---------------------------------------------------------------------------

func TestParseFile_Valid(t *testing.T) {
	content := []byte(`+++
name = "daily-briefing"
description = "Compile and deliver a daily briefing"
version = "1.0.0"
triggers = ["schedule:daily:08:00", "command:briefing"]

[requires]
tools = ["web-search", "calendar"]
+++

# Daily Briefing

When triggered, compile a briefing.`)

	s, err := ParseFile("test.md", content)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if s.Name != "daily-briefing" {
		t.Errorf("Name = %q, want daily-briefing", s.Name)
	}
	if s.Description != "Compile and deliver a daily briefing" {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Version != "1.0.0" {
		t.Errorf("Version = %q, want 1.0.0", s.Version)
	}
	if len(s.Triggers) != 2 || s.Triggers[0] != "schedule:daily:08:00" {
		t.Errorf("Triggers = %v", s.Triggers)
	}
	if len(s.Requires.Tools) != 2 || s.Requires.Tools[0] != "web-search" {
		t.Errorf("Requires.Tools = %v", s.Requires.Tools)
	}
	if !strings.Contains(s.Body, "Daily Briefing") {
		t.Errorf("Body does not contain expected content: %q", s.Body)
	}
	if s.Source != "test.md" {
		t.Errorf("Source = %q, want test.md", s.Source)
	}
}

func TestParseFile_MinimalFrontmatter(t *testing.T) {
	content := []byte(`+++
name = "simple"
+++

Do something simple.`)

	s, err := ParseFile("simple.md", content)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	if s.Name != "simple" {
		t.Errorf("Name = %q, want simple", s.Name)
	}
	if s.Description != "" {
		t.Errorf("Description should be empty, got %q", s.Description)
	}
	if s.Version != "" {
		t.Errorf("Version should be empty, got %q", s.Version)
	}
	if len(s.Triggers) != 0 {
		t.Errorf("Triggers should be empty, got %v", s.Triggers)
	}
	if s.Body != "Do something simple." {
		t.Errorf("Body = %q, want 'Do something simple.'", s.Body)
	}
}

func TestParseFile_MissingOpeningDelimiter(t *testing.T) {
	content := []byte(`name = "missing-delimiters"

Some body.`)

	_, err := ParseFile("bad.md", content)
	if err == nil {
		t.Fatal("expected error for missing opening +++, got nil")
	}
}

func TestParseFile_MissingClosingDelimiter(t *testing.T) {
	content := []byte(`+++
name = "unclosed"

Body here.`)

	_, err := ParseFile("unclosed.md", content)
	if err == nil {
		t.Fatal("expected error for missing closing +++, got nil")
	}
}

func TestParseFile_MissingName(t *testing.T) {
	content := []byte(`+++
description = "no name here"
+++

Body.`)

	_, err := ParseFile("noname.md", content)
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
}

func TestParseFile_InvalidTOML(t *testing.T) {
	content := []byte(`+++
name = [unclosed array
+++

Body.`)

	_, err := ParseFile("bad-toml.md", content)
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
}

// ---------------------------------------------------------------------------
// LoadDir tests
// ---------------------------------------------------------------------------

func TestLoadDir_NonexistentDir(t *testing.T) {
	skills, err := LoadDir("/nonexistent/path/that/does/not/exist", discardLogger())
	if err != nil {
		t.Fatalf("expected no error for nonexistent dir, got: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected empty slice, got %d skills", len(skills))
	}
}

func TestLoadDir_SingleFile(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "greet.md"), []byte(`+++
name = "greet"
description = "Say hello"
+++

Say hello to the user.`), 0o644); err != nil {
		t.Fatalf("writing skill file: %v", err)
	}

	skills, err := LoadDir(dir, discardLogger())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}
	if skills[0].Name != "greet" {
		t.Errorf("Name = %q, want greet", skills[0].Name)
	}
}

func TestLoadDir_SubdirWithSkillMd(t *testing.T) {
	dir := t.TempDir()

	subdir := filepath.Join(dir, "smart-home")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "SKILL.md"), []byte(`+++
name = "smart-home"
description = "Control smart home devices"
+++

Control lights and thermostats.`), 0o644); err != nil {
		t.Fatalf("writing SKILL.md: %v", err)
	}

	skills, err := LoadDir(dir, discardLogger())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}
	if skills[0].Name != "smart-home" {
		t.Errorf("Name = %q, want smart-home", skills[0].Name)
	}
}

func TestLoadDir_SkipsInvalidFiles(t *testing.T) {
	dir := t.TempDir()

	// Valid skill
	if err := os.WriteFile(filepath.Join(dir, "valid.md"), []byte(`+++
name = "valid"
+++

Valid body.`), 0o644); err != nil {
		t.Fatalf("writing valid.md: %v", err)
	}

	// Invalid skill (missing closing +++)
	if err := os.WriteFile(filepath.Join(dir, "invalid.md"), []byte(`+++
name = "invalid"

No closing delimiter.`), 0o644); err != nil {
		t.Fatalf("writing invalid.md: %v", err)
	}

	skills, err := LoadDir(dir, discardLogger())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1 (invalid file should be skipped)", len(skills))
	}
	if skills[0].Name != "valid" {
		t.Errorf("Name = %q, want valid", skills[0].Name)
	}
}

func TestLoadDir_IgnoresNonMdFiles(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a skill"), 0o644); err != nil {
		t.Fatalf("writing notes.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte(`+++
name = "real"
+++

Real skill.`), 0o644); err != nil {
		t.Fatalf("writing skill.md: %v", err)
	}

	skills, err := LoadDir(dir, discardLogger())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "real" {
		t.Errorf("got %+v, want one skill named 'real'", skills)
	}
}

// ---------------------------------------------------------------------------
// BuildPromptSection tests
// ---------------------------------------------------------------------------

func TestBuildPromptSection_Empty(t *testing.T) {
	result := BuildPromptSection(nil)
	if result != "" {
		t.Errorf("expected empty string for empty slice, got %q", result)
	}
	result = BuildPromptSection([]Skill{})
	if result != "" {
		t.Errorf("expected empty string for empty slice, got %q", result)
	}
}

func TestBuildPromptSection_Multiple(t *testing.T) {
	skills := []Skill{
		{Name: "greet", Description: "Say hello", Body: "Greet the user warmly."},
		{Name: "farewell", Description: "", Body: "Say goodbye."},
	}

	result := BuildPromptSection(skills)

	if !strings.Contains(result, "# Skills") {
		t.Error("missing '# Skills' header")
	}
	if !strings.Contains(result, "## greet") {
		t.Error("missing '## greet' section")
	}
	if !strings.Contains(result, "Say hello") {
		t.Error("missing greet description")
	}
	if !strings.Contains(result, "Greet the user warmly.") {
		t.Error("missing greet body")
	}
	if !strings.Contains(result, "## farewell") {
		t.Error("missing '## farewell' section")
	}
	if !strings.Contains(result, "Say goodbye.") {
		t.Error("missing farewell body")
	}

	// Sections should appear in order
	greetIdx := strings.Index(result, "## greet")
	farewellIdx := strings.Index(result, "## farewell")
	if greetIdx > farewellIdx {
		t.Error("greet should appear before farewell")
	}
}
