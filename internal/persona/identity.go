package persona

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ParseIdentity parses IDENTITY.md content into frontmatter fields and body.
func ParseIdentity(content string) (*Identity, error) { return parseIdentity(content) }

// plainScalar matches values safe to write as a bare YAML scalar: no
// leading indicator, no ": " or " #", no quotes. Anything else is quoted.
var plainScalar = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ,.'!?()/+&-]*$`)

// yamlScalar renders s as a YAML scalar a person can read. Go's quoting
// keeps printable Unicode (yaml.v3 would write an emoji as \U0001F98A) and
// its escapes are all valid in YAML double-quoted strings.
func yamlScalar(s string) string {
	if plainScalar.MatchString(s) && strings.TrimSpace(s) == s {
		return s
	}
	return strconv.Quote(s)
}

// FormatIdentity renders id as IDENTITY.md content: YAML frontmatter, then
// the body. Values are quoted when needed, so quotes, colons or a newline in
// the theme stay data instead of becoming new keys. Name and emoji must be
// one line.
func FormatIdentity(id Identity) (string, error) {
	if strings.ContainsAny(id.Name, "\r\n") || strings.ContainsAny(id.Emoji, "\r\n") {
		return "", errors.New("persona: identity name and emoji must be a single line")
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + yamlScalar(id.Name) + "\n")
	b.WriteString("emoji: " + strconv.Quote(id.Emoji) + "\n")
	b.WriteString("theme: " + yamlScalar(id.Theme) + "\n")
	b.WriteString("---\n")
	// Fail closed: the output must parse back to exactly these values.
	if got, err := parseIdentity(b.String()); err != nil || got.Name != id.Name || got.Emoji != id.Emoji || got.Theme != id.Theme {
		return "", fmt.Errorf("persona: identity does not round-trip: %v", err)
	}
	if body := strings.TrimSpace(id.Body); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String(), nil
}
