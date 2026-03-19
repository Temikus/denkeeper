package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPrompt is the fallback system prompt when no persona files are available.
const DefaultPrompt = "You are Foxbox, a helpful personal AI assistant."

// Persona holds the content of persona definition files.
type Persona struct {
	Soul    string // SOUL.md content (required)
	User    string // USER.md content (optional)
	Context string // CONTEXT.md content (optional)
}

// Load reads persona files from the given directory.
// SOUL.md is required and must be non-empty. USER.md and CONTEXT.md are optional.
func Load(dir string) (*Persona, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("persona: opening directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("persona: %s is not a directory", dir)
	}

	soul, err := os.ReadFile(filepath.Join(dir, "SOUL.md"))
	if err != nil {
		return nil, fmt.Errorf("persona: reading SOUL.md: %w", err)
	}
	if strings.TrimSpace(string(soul)) == "" {
		return nil, fmt.Errorf("persona: SOUL.md is empty")
	}

	p := &Persona{
		Soul: strings.TrimSpace(string(soul)),
	}

	if data, err := os.ReadFile(filepath.Join(dir, "USER.md")); err == nil {
		p.User = strings.TrimSpace(string(data))
	}

	if data, err := os.ReadFile(filepath.Join(dir, "CONTEXT.md")); err == nil {
		p.Context = strings.TrimSpace(string(data))
	}

	return p, nil
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

	if p.Context != "" {
		b.WriteString("\n\n# Context\n\n")
		b.WriteString(p.Context)
	}

	return b.String()
}
