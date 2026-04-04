package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPrompt is the fallback system prompt when no persona files are available.
const DefaultPrompt = "You are Denkeeper, a helpful personal AI assistant."

// Persona holds the content of persona definition files.
type Persona struct {
	dir    string // directory where persona files live; empty = read-only
	Soul   string // SOUL.md content (required)
	User   string // USER.md content (optional)
	Memory string // MEMORY.md content (optional)

	// Editable tracks which sections the agent can modify without elevated permissions.
	// true = freely writable; false = requires approval or elevated permissions.
	Editable map[string]bool
}

// Dir returns the directory the persona was loaded from.
// Empty string means no write path is available.
func (p *Persona) Dir() string { return p.dir }

// Load reads persona files from the given directory.
// SOUL.md is required and must be non-empty. USER.md and MEMORY.md are optional.
func Load(dir string) (*Persona, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("persona: opening directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("persona: %s is not a directory", dir)
	}

	soul, err := os.ReadFile(filepath.Join(dir, "SOUL.md")) // #nosec G304 -- dir from config, filenames are constants
	if err != nil {
		return nil, fmt.Errorf("persona: reading SOUL.md: %w", err)
	}
	if strings.TrimSpace(string(soul)) == "" {
		return nil, fmt.Errorf("persona: SOUL.md is empty")
	}

	p := &Persona{
		dir:  dir,
		Soul: strings.TrimSpace(string(soul)),
		Editable: map[string]bool{
			"soul":   true, // editable by user via dashboard/API; agent access governed by permission tier
			"user":   true, // editable by user via dashboard/API; agent access governed by permission tier
			"memory": true, // editable by user via dashboard/API; agent writes freely
		},
	}

	if data, err := os.ReadFile(filepath.Join(dir, "USER.md")); err == nil { // #nosec G304 -- dir from config, filenames are constants
		p.User = strings.TrimSpace(string(data))
	}

	if data, err := os.ReadFile(filepath.Join(dir, "MEMORY.md")); err == nil { // #nosec G304 -- dir from config, filenames are constants
		p.Memory = strings.TrimSpace(string(data))
	}

	return p, nil
}

// Save writes content to the named section file atomically and updates the
// in-memory state. section must be one of "memory", "user", or "soul".
// Returns an error if the persona was not loaded from a directory.
func (p *Persona) Save(section, content string) error {
	if p.dir == "" {
		return fmt.Errorf("persona: no directory set, cannot save %q section", section)
	}
	filename, err := sectionFilename(section)
	if err != nil {
		return err
	}
	target := filepath.Join(p.dir, filename)
	tmp := target + ".tmp"
	trimmed := strings.TrimSpace(content)
	if err := os.WriteFile(tmp, []byte(trimmed+"\n"), 0600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persona: writing %s: %w", filename, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persona: committing %s: %w", filename, err)
	}
	switch strings.ToLower(section) {
	case "memory":
		p.Memory = trimmed
	case "user":
		p.User = trimmed
	case "soul":
		p.Soul = trimmed
	}
	return nil
}

// UpdateMemory replaces MEMORY.md with the given content.
// It is shorthand for Save("memory", content).
func (p *Persona) UpdateMemory(content string) error {
	return p.Save("memory", content)
}

// sectionFilename maps a section name to its filename.
func sectionFilename(section string) (string, error) {
	switch strings.ToLower(section) {
	case "memory":
		return "MEMORY.md", nil
	case "user":
		return "USER.md", nil
	case "soul":
		return "SOUL.md", nil
	default:
		return "", fmt.Errorf("persona: unknown section %q, must be one of: memory, user, soul", section)
	}
}

// MemoryUpdateInstruction returns the system prompt fragment that instructs the
// agent how to signal a memory update. Returns an empty string if no write path
// is available (dir not set or memory is not editable).
func (p *Persona) MemoryUpdateInstruction() string {
	if p.dir == "" || !p.IsEditable("memory") {
		return ""
	}
	return `## Memory Updates

If important context emerges during this conversation that you should remember for future sessions, include a memory update block at the end of your response:

[MEMORY_UPDATE]
<complete updated MEMORY.md content>
[/MEMORY_UPDATE]

The content between the tags replaces MEMORY.md entirely — preserve any existing context you want to keep. Only include this when genuinely useful information needs to persist across sessions. Omit it entirely when no update is needed.`
}

// UserUpdateInstruction returns the system prompt fragment that instructs the
// agent how to request a USER.md update via a [USER_UPDATE] directive.
// Returns an empty string if the persona has no write path or the tier is
// "restricted" (which cannot write user files).
func (p *Persona) UserUpdateInstruction(tier string) string {
	if p.dir == "" || tier == "restricted" {
		return ""
	}
	var modeNote string
	if tier == "autonomous" {
		modeNote = "In autonomous mode, this will be applied directly."
	} else {
		modeNote = "In supervised mode, this will be presented for your approval before being applied."
	}
	return `## User Profile Updates

If the user shares important personal information they want remembered persistently ` +
		`(name, preferences, background details, routines), include a user update block ` +
		`at the end of your response:

[USER_UPDATE]
<complete updated USER.md content>
[/USER_UPDATE]

` + modeNote + ` Only include this when the user explicitly shares information they want ` +
		`persisted across sessions. Omit entirely when not needed.`
}

// IsEditable reports whether the section can be edited via the dashboard/API by the user.
// Unknown sections are treated as not editable (returns false).
func (p *Persona) IsEditable(section string) bool {
	ed, ok := p.Editable[strings.ToLower(section)]
	return ok && ed
}

// IsAgentMutable reports whether the agent itself can modify the given section.
// "memory" is always agent-mutable; "user" requires supervised/autonomous tier
// (via approval or directive); "soul" is never agent-mutable.
func (p *Persona) IsAgentMutable(section string) bool {
	switch strings.ToLower(section) {
	case "memory":
		return true
	case "user":
		return true // via USER_UPDATE directive (supervised tier requires approval)
	case "soul":
		return false
	default:
		return false
	}
}

// SystemPrompt assembles the persona into a single system prompt string.
func (p *Persona) SystemPrompt() string {
	var b strings.Builder

	b.WriteString("# Soul\n\n")
	b.WriteString(p.Soul)

	if p.User != "" {
		b.WriteString("\n\n# User\n\n")
		b.WriteString(p.User)
	}

	if p.Memory != "" {
		b.WriteString("\n\n# Memory\n\n")
		b.WriteString(p.Memory)
	}

	return b.String()
}
