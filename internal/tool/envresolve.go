package tool

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// placeholderRegexp matches ${NAME} placeholders in strings.
var placeholderRegexp = regexp.MustCompile(`\$\{([^}]+)\}`)

// forbiddenEnvPatterns lists env var name patterns that must never be resolved
// into tool configurations (prevents leaking internal secrets into subprocess
// environments or remote URLs).
// Patterns use simple glob-style matching: * matches any substring.
var forbiddenEnvPatterns = []string{
	"DENKEEPER_*_SECRET",
	"DENKEEPER_*_PASSWORD*",
	"DENKEEPER_DB_KEY",
	"DENKEEPER_OIDC_CLIENT_SECRET",
}

// resolveEnvPlaceholders replaces ${NAME} placeholders in value.
// Resolution order: toolEnv map (if non-nil) → os.Getenv.
// Returns an error if a placeholder references a forbidden env var pattern.
// Unresolvable placeholders are replaced with empty string.
func resolveEnvPlaceholders(value string, toolEnv map[string]string) (string, error) {
	var resolveErr error

	result := placeholderRegexp.ReplaceAllStringFunc(value, func(match string) string {
		if resolveErr != nil {
			return match // stop processing on first error
		}
		name := match[2 : len(match)-1] // strip ${ and }

		if isForbiddenEnvVar(name) {
			resolveErr = fmt.Errorf("env var %q matches a forbidden pattern and cannot be used in tool config", name)
			return match
		}

		// Try tool-specific env first.
		if toolEnv != nil {
			if v, ok := toolEnv[name]; ok {
				return v
			}
		}
		return os.Getenv(name)
	})

	if resolveErr != nil {
		return "", resolveErr
	}
	return result, nil
}

// isForbiddenEnvVar checks whether an env var name matches any forbidden pattern.
func isForbiddenEnvVar(name string) bool {
	upper := strings.ToUpper(name)
	for _, pattern := range forbiddenEnvPatterns {
		if matchGlob(upper, strings.ToUpper(pattern)) {
			return true
		}
	}
	return false
}

// matchGlob performs simple glob matching where * matches any substring.
func matchGlob(s, pattern string) bool {
	// Split pattern on *.
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return s == pattern
	}

	// First part must be a prefix.
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]

	// Middle parts must appear in order.
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}

	// Last part must be a suffix.
	return strings.HasSuffix(s, parts[len(parts)-1])
}
