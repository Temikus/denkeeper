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
//
// The write lock is held for the entire operation (file I/O + in-memory update)
// so that read-modify-write callers like AppendMemoryEntry can use saveLocked
// without a race window. This means concurrent readers (GetMemory, etc.) block
// during file I/O — acceptable at current concurrency levels but worth revisiting
// if persona access ever becomes a hot path.
func (p *Persona) Save(section, content string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.saveLocked(section, content)
}

// saveLocked performs the actual save. Caller must hold p.mu write lock.
func (p *Persona) saveLocked(section, content string) error {
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

// AppendMemoryEntry reads the current MEMORY.md, appends a new entry separated
// by "---", and writes the result back. If memory is currently empty the entry
// is written without a separator. The entire read-modify-write is atomic.
func (p *Persona) AppendMemoryEntry(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return fmt.Errorf("persona: empty memory entry")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var updated string
	if p.memory == "" {
		updated = entry
	} else {
		updated = p.memory + "\n\n---\n\n" + entry
	}
	return p.saveLocked("memory", updated)
}

// RemoveMemoryEntry finds and removes a memory entry whose first line starts
// with "## <heading>" (case-insensitive match). Entries are separated by blank-
// line-surrounded "---" lines. Returns an error if no matching entry is found.
// The entire read-modify-write is atomic.
func (p *Persona) RemoveMemoryEntry(heading string) error {
	heading = strings.TrimSpace(heading)
	if heading == "" {
		return fmt.Errorf("persona: empty heading")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.memory == "" {
		return fmt.Errorf("persona: memory is empty, nothing to remove")
	}

	parts := splitMemoryEntries(p.memory)
	target := strings.ToLower("## " + heading)

	var kept []string
	found := false
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		firstLine := trimmed
		if idx := strings.IndexByte(trimmed, '\n'); idx != -1 {
			firstLine = trimmed[:idx]
		}
		if strings.ToLower(strings.TrimSpace(firstLine)) == target {
			found = true
			continue
		}
		kept = append(kept, trimmed)
	}
	if !found {
		return fmt.Errorf("persona: no memory entry with heading %q found", heading)
	}

	updated := strings.Join(kept, "\n\n---\n\n")
	return p.saveLocked("memory", updated)
}

// splitMemoryEntries splits memory content on "---" separator lines that are
// surrounded by blank lines (the format produced by AppendMemoryEntry). A lone
// "---" at the very start or end of the content also counts as a separator.
// This avoids splitting on YAML frontmatter delimiters or markdown horizontal
// rules that appear inline.
func splitMemoryEntries(content string) []string {
	lines := strings.Split(content, "\n")
	n := len(lines)
	var parts []string
	var current []string

	for i, line := range lines {
		if strings.TrimSpace(line) != "---" {
			current = append(current, line)
			continue
		}
		// Check if this "---" is a memory separator: preceded by a blank
		// line (or at start) AND followed by a blank line (or at end).
		prevBlank := i == 0 || strings.TrimSpace(lines[i-1]) == ""
		nextBlank := i == n-1 || strings.TrimSpace(lines[i+1]) == ""
		if prevBlank && nextBlank {
			if len(current) > 0 {
				parts = append(parts, strings.Join(current, "\n"))
				current = nil
			}
		} else {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		parts = append(parts, strings.Join(current, "\n"))
	}
	return parts
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
