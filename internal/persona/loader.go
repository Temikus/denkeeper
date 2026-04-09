package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Identity holds the parsed YAML frontmatter and body from IDENTITY.md.
type Identity struct {
	Name  string `yaml:"name"`
	Emoji string `yaml:"emoji"`
	Theme string `yaml:"theme"`
	Body  string `yaml:"-"` // markdown body after frontmatter
}

// DefaultPrompt is the fallback system prompt when no persona files are available.
const DefaultPrompt = "You are Denkeeper, a helpful personal AI assistant."

// Persona holds the content of persona definition files. All exported fields
// are guarded by mu; callers must use the accessor methods or hold the lock.
type Persona struct {
	mu  sync.RWMutex
	dir string // directory where persona files live; empty = read-only

	// Guarded by mu — use accessor methods for concurrent access.
	soul        string // SOUL.md content (required)
	user        string // USER.md content (optional)
	memory      string // MEMORY.md content (optional)
	identityRaw string
	identity    *Identity

	// Editable tracks which sections the agent can modify without elevated permissions.
	// true = freely writable; false = requires approval or elevated permissions.
	Editable map[string]bool
}

// Dir returns the directory the persona was loaded from.
// Empty string means no write path is available.
func (p *Persona) Dir() string { return p.dir }

// GetSoul returns the SOUL.md content.
func (p *Persona) GetSoul() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.soul
}

// GetUser returns the USER.md content.
func (p *Persona) GetUser() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.user
}

// GetMemory returns the MEMORY.md content.
func (p *Persona) GetMemory() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.memory
}

// GetIdentityRaw returns the raw IDENTITY.md content for API round-tripping.
func (p *Persona) GetIdentityRaw() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.identityRaw
}

// GetIdentity returns the parsed Identity (may be nil).
func (p *Persona) GetIdentity() *Identity {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.identity
}

// Sections returns which sections have content, keyed by section name.
func (p *Persona) Sections() map[string]bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]bool{
		"identity": p.identityRaw != "",
		"soul":     p.soul != "",
		"user":     p.user != "",
		"memory":   p.memory != "",
	}
}

// GetSection returns the content for the given section, whether the section
// is user-editable, whether the agent can mutate it, and whether it exists.
func (p *Persona) GetSection(section string) (content string, editable, agentMutable, ok bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	switch strings.ToLower(section) {
	case "identity":
		return p.identityRaw, p.IsEditableUnlocked(section), p.IsAgentMutable(section), true
	case "soul":
		return p.soul, p.IsEditableUnlocked(section), p.IsAgentMutable(section), true
	case "user":
		return p.user, p.IsEditableUnlocked(section), p.IsAgentMutable(section), true
	case "memory":
		return p.memory, p.IsEditableUnlocked(section), p.IsAgentMutable(section), true
	default:
		return "", false, false, false
	}
}

// IsEditableUnlocked is like IsEditable but does not acquire the lock.
// Caller must hold at least mu.RLock.
func (p *Persona) IsEditableUnlocked(section string) bool {
	ed, ok := p.Editable[strings.ToLower(section)]
	return ok && ed
}

// NewEmpty creates a writable Persona with no content. The directory is created
// on first Save if it does not exist. Use this when the persona directory is
// known but no files exist yet, so the user can create sections from the dashboard.
func NewEmpty(dir string) *Persona {
	return &Persona{
		dir: dir,
		Editable: map[string]bool{
			"identity": true,
			"soul":     true,
			"user":     true,
			"memory":   true,
		},
	}
}

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
		soul: strings.TrimSpace(string(soul)),
		Editable: map[string]bool{
			"identity": true, // editable by user via dashboard/API; agent access governed by permission tier
			"soul":     true, // editable by user via dashboard/API; agent access governed by permission tier
			"user":     true, // editable by user via dashboard/API; agent access governed by permission tier
			"memory":   true, // editable by user via dashboard/API; agent writes freely
		},
	}

	if data, err := os.ReadFile(filepath.Join(dir, "IDENTITY.md")); err == nil { // #nosec G304 -- dir from config, filenames are constants
		raw := strings.TrimSpace(string(data))
		p.identityRaw = raw
		if id, parseErr := parseIdentity(raw); parseErr == nil {
			p.identity = id
		}
	}

	if data, err := os.ReadFile(filepath.Join(dir, "USER.md")); err == nil { // #nosec G304 -- dir from config, filenames are constants
		p.user = strings.TrimSpace(string(data))
	}

	if data, err := os.ReadFile(filepath.Join(dir, "MEMORY.md")); err == nil { // #nosec G304 -- dir from config, filenames are constants
		p.memory = strings.TrimSpace(string(data))
	}

	return p, nil
}

// Save writes content to the named section file atomically and updates the
// in-memory state. section must be one of "identity", "memory", "user", or "soul".
// Returns an error if the persona was not loaded from a directory.
func (p *Persona) Save(section, content string) error {
	if p.dir == "" {
		return fmt.Errorf("persona: no directory set, cannot save %q section", section)
	}
	filename, err := sectionFilename(section)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.dir, 0700); err != nil {
		return fmt.Errorf("persona: creating directory %s: %w", p.dir, err)
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

	// Update in-memory state under the write lock.
	p.mu.Lock()
	defer p.mu.Unlock()
	switch strings.ToLower(section) {
	case "identity":
		p.identityRaw = trimmed
		if id, parseErr := parseIdentity(trimmed); parseErr == nil {
			p.identity = id
		}
	case "memory":
		p.memory = trimmed
	case "user":
		p.user = trimmed
	case "soul":
		p.soul = trimmed
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
	case "identity":
		return "IDENTITY.md", nil
	case "memory":
		return "MEMORY.md", nil
	case "user":
		return "USER.md", nil
	case "soul":
		return "SOUL.md", nil
	default:
		return "", fmt.Errorf("persona: unknown section %q, must be one of: identity, memory, user, soul", section)
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

// SoulUpdateInstruction returns the system prompt fragment that instructs the
// agent how to request a SOUL.md update via a [SOUL_UPDATE] directive.
// Returns an empty string if the persona has no write path or the tier is
// "restricted" (which cannot write soul files).
func (p *Persona) SoulUpdateInstruction(tier string) string {
	if p.dir == "" || tier == "restricted" {
		return ""
	}
	var modeNote string
	if tier == "autonomous" {
		modeNote = "In autonomous mode, this will be applied directly."
	} else {
		modeNote = "In supervised mode, this will be presented for your approval before being applied."
	}
	return `---

_This file is yours to evolve. As you learn who you are, update it._

## Soul Evolution

If your core identity, values, or personality should evolve based on your experiences ` +
		`and growth, include a soul update block at the end of your response:

[SOUL_UPDATE]
<complete updated SOUL.md content>
[/SOUL_UPDATE]

` + modeNote + ` Only include this when you have a genuine reason to evolve your identity. ` +
		`Omit entirely when not needed.`
}

// IdentityUpdateInstruction returns the system prompt fragment that instructs the
// agent how to request an IDENTITY.md update via an [IDENTITY_UPDATE] directive.
// Returns an empty string if the persona has no write path or the tier is
// "restricted" (which cannot write identity files).
func (p *Persona) IdentityUpdateInstruction(tier string) string {
	if p.dir == "" || tier == "restricted" {
		return ""
	}
	var modeNote string
	if tier == "autonomous" {
		modeNote = "In autonomous mode, this will be applied directly."
	} else {
		modeNote = "In supervised mode, this will be presented for your approval before being applied."
	}
	return `## Identity Updates

If your name, emoji, theme, or identity metadata should change, include an identity update block ` +
		`at the end of your response. Preserve the YAML frontmatter format:

[IDENTITY_UPDATE]
---
name: YourName
emoji: "🤖"
theme: your vibe description
---

Any additional identity notes here.
[/IDENTITY_UPDATE]

` + modeNote + ` Only include this when your identity metadata genuinely needs updating. ` +
		`Omit entirely when not needed.`
}

// parseIdentity parses IDENTITY.md content: optional YAML frontmatter delimited
// by "---" lines, followed by a markdown body.
func parseIdentity(content string) (*Identity, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return &Identity{}, nil
	}

	id := &Identity{}

	if !strings.HasPrefix(content, "---") {
		id.Body = content
		return id, nil
	}

	// Find the closing "---" after the opening one.
	rest := content[3:] // skip opening "---"
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}

	closeIdx := strings.Index(rest, "\n---")
	if closeIdx == -1 {
		// No closing delimiter — treat entire content as body.
		id.Body = content
		return id, nil
	}

	yamlBlock := rest[:closeIdx]
	afterClose := rest[closeIdx+4:] // skip "\n---"

	if err := yaml.Unmarshal([]byte(yamlBlock), id); err != nil {
		return nil, fmt.Errorf("persona: parsing IDENTITY.md frontmatter: %w", err)
	}

	id.Body = strings.TrimSpace(afterClose)
	return id, nil
}

// IsEditable reports whether the section can be edited via the dashboard/API by the user.
// Unknown sections are treated as not editable (returns false).
func (p *Persona) IsEditable(section string) bool {
	ed, ok := p.Editable[strings.ToLower(section)]
	return ok && ed
}

// IsAgentMutable reports whether the agent itself can modify the given section.
// "memory" is always agent-mutable; "identity", "user" and "soul" require
// supervised/autonomous tier (via approval or directive).
func (p *Persona) IsAgentMutable(section string) bool {
	switch strings.ToLower(section) {
	case "memory":
		return true
	case "identity":
		return true // via IDENTITY_UPDATE directive (supervised tier requires approval)
	case "user":
		return true // via USER_UPDATE directive (supervised tier requires approval)
	case "soul":
		return true // via SOUL_UPDATE directive (supervised tier requires approval)
	default:
		return false
	}
}

// SystemPrompt assembles the persona into a single system prompt string.
func (p *Persona) SystemPrompt() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var b strings.Builder

	if p.identity != nil {
		b.WriteString("# Identity\n\n")
		if p.identity.Name != "" {
			line := "Your name is " + p.identity.Name
			if p.identity.Emoji != "" {
				line += " " + p.identity.Emoji
			}
			b.WriteString(line + ".\n")
		}
		if p.identity.Theme != "" {
			b.WriteString("Your vibe: " + p.identity.Theme + ".\n")
		}
		if p.identity.Body != "" {
			b.WriteString("\n" + p.identity.Body)
		}
		b.WriteString("\n\n")
	}

	b.WriteString("# Soul\n\n")
	b.WriteString(p.soul)

	if p.user != "" {
		b.WriteString("\n\n# User\n\n")
		b.WriteString(p.user)
	}

	if p.memory != "" {
		b.WriteString("\n\n# Memory\n\n")
		b.WriteString(p.memory)
	}

	return b.String()
}
