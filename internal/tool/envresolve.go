package tool

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// placeholderRegexp matches ${NAME} placeholders in strings.
var placeholderRegexp = regexp.MustCompile(`\$\{([^}]+)\}`)

// envRefRegexp matches $NAME and ${NAME}, the forms os.ExpandEnv expands in
// tool and plugin env values when the config is loaded.
var envRefRegexp = regexp.MustCompile(`\$(\{[^}]*\}|[A-Za-z_][A-Za-z0-9_]*)`)

// ValidateUntrustedEnvRefs rejects a tool or plugin config, written by an agent
// or a non-admin key, that would pull the host environment in. Env values may
// not reference variables, and ${NAME} in url or headers must name a key of
// env. Otherwise ${DENKEEPER_LLM_ANTHROPIC_API_KEY} in a URL sends the key
// off-box. Operators use TOML or env_passthrough instead.
func ValidateUntrustedEnvRefs(env map[string]string, url string, headers map[string]string) error {
	for k, v := range env {
		if ref := envRefRegexp.FindString(v); ref != "" {
			return fmt.Errorf("env %q references host variable %s; only an operator can do that", k, ref)
		}
	}
	check := func(field, value string) error {
		for _, m := range placeholderRegexp.FindAllStringSubmatch(value, -1) {
			if _, ok := env[m[1]]; !ok {
				return fmt.Errorf("%s references ${%s}, which is not in this tool's env; only an operator can reference host variables", field, m[1])
			}
		}
		return nil
	}
	if err := check("url", url); err != nil {
		return err
	}
	for k, v := range headers {
		if err := check(fmt.Sprintf("header %q", k), v); err != nil {
			return err
		}
	}
	return nil
}

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
