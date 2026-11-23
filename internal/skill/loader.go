// Package skill loads and parses flat-file skill definitions.
// Skills are markdown files with frontmatter: TOML between +++ lines, or YAML
// between --- lines. A skill is written back in the dialect it was read in.
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
)

// SkillRequires lists optional capabilities a skill depends on.
type SkillRequires struct {
	Tools []string `toml:"tools" yaml:"tools"`
}

// Skill represents a loaded skill file.
type Skill struct {
	Name           string
	Description    string
	Version        string
	Triggers       []string  // raw trigger strings from frontmatter — informational only
	ParsedTriggers []Trigger // parsed at load time; the sole input to MatchSkills
	Requires       SkillRequires
	Body           string // markdown body — everything after the closing +++
	Source         string // file path, for logging/debugging

	// MaxToolRounds optionally lowers the engine's per-turn tool-round budget
	// for turns this skill explicitly drives (a scheduled run, or a lone
	// command match). The unit is *rounds*, not calls — one round may fan out
	// to many parallel tool calls, matching the agent-level max_tool_rounds
	// knob it tightens. 0 means unset (the agent/engine budget applies), and
	// the cap only ever lowers that budget: an operator's agent-level ceiling
	// is never widened by a skill, which the agent itself can author.
	MaxToolRounds int

	// MaxOutputTokens optionally caps output tokens per completion for turns
	// this skill drives, under the same name and unit as the agent-level
	// max_output_tokens knob. 0 means unset. The value lowers a cap set on the
	// agent or in [llm], and is sent as is when neither sets one.
	MaxOutputTokens int

	// SubFileNames lists relative paths to sub-files within a subdirectory-form
	// skill (e.g. "references/oauth.md", "templates/greeting.txt"). Sorted.
	// Empty for flat-file skills.
	SubFileNames []string

	// Dir is the skill directory path. Non-empty only for subdirectory-form
	// skills (containing SKILL.md). Empty for flat-file skills.
	Dir string

	// Dialect is the frontmatter dialect the skill was parsed from; empty
	// means TOML. Frontmatter is the raw block between the delimiters, kept
	// so an edit can write it back unchanged. Both stay off JSON output.
	Dialect     Dialect `json:"-"`
	Frontmatter string  `json:"-"`
}

// frontmatter is the decoded structure of the known frontmatter keys.
type frontmatter struct {
	Name            string        `toml:"name" yaml:"name"`
	Description     string        `toml:"description" yaml:"description"`
	Version         string        `toml:"version" yaml:"version"`
	Triggers        []string      `toml:"triggers" yaml:"triggers"`
	Requires        SkillRequires `toml:"requires" yaml:"requires"`
	MaxToolRounds   int           `toml:"max_tool_rounds" yaml:"max_tool_rounds"`
	MaxOutputTokens int           `toml:"max_output_tokens" yaml:"max_output_tokens"`
}

// ParseFile parses the content of a single skill file.
// path is used only for the Source field and error messages.
// Returns an error if the frontmatter delimiters are malformed,
// the frontmatter is invalid, or the name field is missing.
func ParseFile(path string, content []byte) (*Skill, error) {
	text := strings.TrimSpace(string(content))

	dialect, block, body, err := splitFrontmatter(path, text)
	if err != nil {
		return nil, err
	}

	var fm frontmatter
	if dialect == DialectYAML {
		err = decodeYAML(path, block, &fm)
	} else {
		err = decodeTOML(path, block, &fm)
	}
	if err != nil {
		return nil, err
	}

	s, err := buildSkill(path, fm, body)
	if err != nil {
		return nil, err
	}
	s.Dialect, s.Frontmatter = dialect, block
	return s, nil
}

// buildSkill applies the checks both dialects share.
func buildSkill(path string, fm frontmatter, body string) (*Skill, error) {
	if strings.TrimSpace(fm.Name) == "" {
		return nil, fmt.Errorf("skill %q: frontmatter missing required field: name", path)
	}

	parsed, err := ParseTriggers(fm.Triggers)
	if err != nil {
		return nil, fmt.Errorf("skill %q: %w", path, err)
	}

	if fm.MaxToolRounds < 0 {
		return nil, fmt.Errorf("skill %q: max_tool_rounds must be >= 0 (0 = unset)", path)
	}
	if fm.MaxOutputTokens < 0 {
		return nil, fmt.Errorf("skill %q: max_output_tokens must be >= 0 (0 = unset)", path)
	}

	return &Skill{
		Name:            strings.TrimSpace(fm.Name),
		Description:     strings.TrimSpace(fm.Description),
		Version:         strings.TrimSpace(fm.Version),
		Triggers:        fm.Triggers,
		ParsedTriggers:  parsed,
		Requires:        fm.Requires,
		MaxToolRounds:   fm.MaxToolRounds,
		MaxOutputTokens: fm.MaxOutputTokens,
		Body:            body,
		Source:          path,
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
