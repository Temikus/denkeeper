// Package skill loads and parses flat-file skill definitions.
// Skills are markdown files with TOML frontmatter enclosed in +++ delimiters.
// They are text-only — no code is executed. Their content is injected into
// the agent's system prompt so the LLM knows what skills are available.
package skill

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// SkillRequires lists optional capabilities a skill depends on.
type SkillRequires struct {
	Tools []string `toml:"tools"`
}

// Skill represents a loaded skill file.
type Skill struct {
	Name           string
	Description    string
	Version        string
	Triggers       []string  // raw trigger strings from frontmatter
	ParsedTriggers []Trigger // parsed at load time for efficient matching
	Requires       SkillRequires
	Body           string // markdown body — everything after the closing +++
	Source         string // file path, for logging/debugging

	// SubFileNames lists relative paths to sub-files within a subdirectory-form
	// skill (e.g. "references/oauth.md", "templates/greeting.txt"). Sorted.
	// Empty for flat-file skills.
	SubFileNames []string

	// Dir is the skill directory path. Non-empty only for subdirectory-form
	// skills (containing SKILL.md). Empty for flat-file skills.
	Dir string
}

// NewTestSkill constructs a Skill with parsed triggers for use in tests.
// Panics if any trigger string is invalid.
func NewTestSkill(name, description string, triggers []string, body string) Skill {
	parsed, err := ParseTriggers(triggers)
	if err != nil {
		panic("skill.NewTestSkill: " + err.Error())
	}
	return Skill{
		Name:           name,
		Description:    description,
		Triggers:       triggers,
		ParsedTriggers: parsed,
		Body:           body,
	}
}

// frontmatter is the TOML-decoded structure of the +++ block.
type frontmatter struct {
	Name        string        `toml:"name"`
	Description string        `toml:"description"`
	Version     string        `toml:"version"`
	Triggers    []string      `toml:"triggers"`
	Requires    SkillRequires `toml:"requires"`
}

// ParseFile parses the content of a single skill file.
// path is used only for the Source field and error messages.
// Returns an error if the frontmatter delimiters are malformed,
// the TOML is invalid, or the name field is missing.
func ParseFile(path string, content []byte) (*Skill, error) {
	text := strings.TrimSpace(string(content))

	if !strings.HasPrefix(text, "+++") {
		return nil, fmt.Errorf("skill %q: missing opening +++ delimiter", path)
	}

	// Strip the opening +++
	rest := text[3:]

	// Find the closing +++
	idx := strings.Index(rest, "+++")
	if idx == -1 {
		return nil, fmt.Errorf("skill %q: missing closing +++ delimiter", path)
	}

	tomlSection := strings.TrimSpace(rest[:idx])
	body := strings.TrimSpace(rest[idx+3:])

	var fm frontmatter
	if err := toml.Unmarshal([]byte(tomlSection), &fm); err != nil {
		return nil, fmt.Errorf("skill %q: parsing frontmatter: %w", path, err)
	}

	if strings.TrimSpace(fm.Name) == "" {
		return nil, fmt.Errorf("skill %q: frontmatter missing required field: name", path)
	}

	parsed, err := ParseTriggers(fm.Triggers)
	if err != nil {
		return nil, fmt.Errorf("skill %q: %w", path, err)
	}

	return &Skill{
		Name:           strings.TrimSpace(fm.Name),
		Description:    strings.TrimSpace(fm.Description),
		Version:        strings.TrimSpace(fm.Version),
		Triggers:       fm.Triggers,
		ParsedTriggers: parsed,
		Requires:       fm.Requires,
		Body:           body,
		Source:         path,
	}, nil
}

// LoadDir scans dir for skill files and returns all valid ones.
// It looks for:
//   - Top-level *.md files
//   - Subdirectories containing a SKILL.md file
//
// A non-existent directory is not an error — it returns an empty slice
// (safe for fresh installs). Files that fail to parse are logged as
// warnings and skipped; other valid files in the directory are still loaded.
func LoadDir(dir string, logger *slog.Logger) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("skill: reading directory %q: %w", dir, err)
	}

	var skills []Skill

	for _, entry := range entries {
		var path string
		var skillDir string

		if entry.IsDir() {
			candidate := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(candidate); err != nil {
				continue // subdir has no SKILL.md — skip
			}
			path = candidate
			skillDir = filepath.Join(dir, entry.Name())
		} else {
			if filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			path = filepath.Join(dir, entry.Name())
		}

		data, err := os.ReadFile(path) // #nosec G304 -- path from persona_dir config walking
		if err != nil {
			logger.Warn("skill: could not read file", "path", path, "error", err)
			continue
		}

		s, err := ParseFile(path, data)
		if err != nil {
			logger.Warn("skill: could not parse file, skipping", "path", path, "error", err)
			continue
		}

		if skillDir != "" {
			s.Dir = skillDir
			s.SubFileNames = ScanSubFiles(skillDir)
		}

		skills = append(skills, *s)
	}

	return skills, nil
}

// AllowedSubdirs lists the permitted subdirectory prefixes within a skill directory.
var AllowedSubdirs = []string{"references", "templates", "scripts"}

// ScanSubFiles walks allowed subdirectories within a skill directory and returns
// sorted relative paths to all regular files found.
func ScanSubFiles(skillDir string) []string {
	var files []string
	for _, subdir := range AllowedSubdirs {
		subEntries, err := os.ReadDir(filepath.Join(skillDir, subdir))
		if err != nil {
			continue
		}
		for _, se := range subEntries {
			if !se.IsDir() {
				files = append(files, subdir+"/"+se.Name())
			}
		}
	}
	sort.Strings(files)
	return files
}

// BuildPromptSection assembles a list of skills into a formatted system-prompt
// section. Returns an empty string if skills is empty.
func BuildPromptSection(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Skills\n")

	for _, s := range skills {
		b.WriteString("\n## ")
		b.WriteString(s.Name)
		b.WriteByte('\n')

		if s.Description != "" {
			b.WriteByte('\n')
			b.WriteString(s.Description)
			b.WriteByte('\n')
		}

		if s.Body != "" {
			b.WriteByte('\n')
			b.WriteString(s.Body)
			b.WriteByte('\n')
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
