package tool

import (
	"log/slog"
	"os"
	"strings"
)

// stdioEnvAllowlist is the set of non-secret parent-process environment variable
// names that are forwarded into stdio MCP subprocesses by default. It covers
// generic system vars, runtime vars MCP servers commonly need (Node, Python,
// Go), and their Windows equivalents. Secret-bearing names are never included
// here; they are additionally excluded by isExcludedEnvVar as a hard backstop.
var stdioEnvAllowlist = map[string]struct{}{
	// Generic POSIX/system.
	"PATH":    {},
	"HOME":    {},
	"TMPDIR":  {},
	"USER":    {},
	"LOGNAME": {},
	"SHELL":   {},
	"LANG":    {},
	"TZ":      {},
	"TERM":    {},
	// Common language-runtime vars MCP servers rely on.
	"NODE_PATH":    {},
	"NODE_OPTIONS": {},
	"PYTHONPATH":   {},
	"VIRTUAL_ENV":  {},
	"GOPATH":       {},
	// Windows equivalents.
	"SYSTEMROOT":   {},
	"COMSPEC":      {},
	"PATHEXT":      {},
	"TEMP":         {},
	"TMP":          {},
	"USERPROFILE":  {},
	"APPDATA":      {},
	"LOCALAPPDATA": {},
	"PROGRAMFILES": {},
}

// isAllowlistedEnvVar reports whether an env var name is in the built-in
// non-secret allowlist. LC_* locale vars are matched by prefix. Matching is
// case-insensitive (uppercase-normalized, mirroring isExcludedEnvVar) because
// Windows env var names are case-insensitive and conventionally mixed-case
// (Path, SystemRoot, ComSpec) — an exact match would silently drop them.
func isAllowlistedEnvVar(name string) bool {
	upper := strings.ToUpper(name)
	if _, ok := stdioEnvAllowlist[upper]; ok {
		return true
	}
	return strings.HasPrefix(upper, "LC_")
}

// isExcludedEnvVar reports whether an env var name must never reach a
// subprocess, regardless of any allowlist or passthrough entry. This is the
// hard secret backstop: everything under the DENKEEPER_* namespace plus the
// forbidden-pattern denylist used for placeholder resolution.
func isExcludedEnvVar(name string) bool {
	if strings.HasPrefix(strings.ToUpper(name), "DENKEEPER_") {
		return true
	}
	return isForbiddenEnvVar(name)
}

// buildStdioEnv constructs the explicit environment for a stdio MCP subprocess.
//
// Instead of inheriting the full secret-bearing parent environment
// (os.Environ()), it forwards only:
//   - names in the built-in non-secret allowlist (stdioEnvAllowlist + LC_*),
//   - names explicitly listed in passthrough ([mcp] env_passthrough merged with
//     the tool's own env_passthrough).
//
// A hard exclusion filter (isExcludedEnvVar) is applied to every forwarded name
// so a secret can never reach the child even if it was allowlisted or passed
// through by accident; a passthrough name that trips the filter is logged.
//
// The tool's own cfg.Env (explicit, already placeholder-resolved) is appended
// last so it wins over any inherited value.
func buildStdioEnv(cfgEnv map[string]string, passthrough []string, logger *slog.Logger) []string {
	// Keyed by uppercase name so passthrough matching is case-insensitive,
	// consistent with the allowlist and exclusion filter (Windows names).
	passSet := make(map[string]struct{}, len(passthrough))
	for _, name := range passthrough {
		if name == "" {
			continue
		}
		if isExcludedEnvVar(name) {
			if logger != nil {
				logger.Warn("env_passthrough entry names a protected variable; refusing to forward it to MCP subprocess", "var", name)
			}
			continue
		}
		passSet[strings.ToUpper(name)] = struct{}{}
	}

	env := make([]string, 0, len(passSet)+len(cfgEnv)+16)
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		name := kv[:eq]

		_, passed := passSet[strings.ToUpper(name)]
		if !passed && !isAllowlistedEnvVar(name) {
			continue
		}
		// Hard backstop: never forward a secret even if allowlisted or passed.
		if isExcludedEnvVar(name) {
			continue
		}
		env = append(env, kv)
	}

	// Tool-specific env is explicit config and wins; appended last so the child
	// process (last-value-wins semantics) uses it over any inherited value.
	for k, v := range cfgEnv {
		env = append(env, k+"="+v)
	}
	return env
}
