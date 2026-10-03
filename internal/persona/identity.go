package persona

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// identityFrontmatter fixes the key order of IDENTITY.md frontmatter. Fields
// are never omitted: an empty mapping would render "{}" and break parsing.
type identityFrontmatter struct {
	Name  string `yaml:"name"`
	Emoji string `yaml:"emoji"`
	Theme string `yaml:"theme"`
}

// ParseIdentity parses IDENTITY.md content into frontmatter fields and body.
func ParseIdentity(content string) (*Identity, error) { return parseIdentity(content) }

// FormatIdentity renders id as IDENTITY.md content: YAML frontmatter, then
// the body. Values are YAML-encoded, so quotes, colons or a newline in the
// theme stay data instead of becoming new keys. Name and emoji must be one line.
func FormatIdentity(id Identity) (string, error) {
	if strings.ContainsAny(id.Name, "\r\n") || strings.ContainsAny(id.Emoji, "\r\n") {
		return "", errors.New("persona: identity name and emoji must be a single line")
	}
	fm, err := yaml.Marshal(identityFrontmatter{Name: id.Name, Emoji: id.Emoji, Theme: id.Theme})
	if err != nil {
		return "", fmt.Errorf("persona: encoding identity frontmatter: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if body := strings.TrimSpace(id.Body); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String(), nil
}
