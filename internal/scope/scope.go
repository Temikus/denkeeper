// Package scope defines the canonical set of API permission scopes.
// All other packages (api, config, CLI) must reference this list
// rather than maintaining their own copies.
package scope

import "sort"

// Valid is the authoritative set of API scope values.
// Every scope used in RequireScope, config validation, CLI flags,
// and the web dashboard must appear here.
var Valid = map[string]struct{}{
	"admin":           {},
	"chat":            {},
	"sessions:read":   {},
	"sessions:write":  {},
	"costs:read":      {},
	"agents:read":     {},
	"agents:write":    {},
	"skills:read":     {},
	"skills:write":    {},
	"schedules:read":  {},
	"schedules:write": {},
	"approvals:read":  {},
	"approvals:write": {},
	"tools:read":      {},
	"tools:write":     {},
	"browser:read":    {},
	"browser:write":   {},
	"kv:read":         {},
	"kv:write":        {},
	"audit:read":      {},
	"channels:read":   {},
	"channels:write":  {},
	"health":          {},
}

// Names returns all valid scope names in sorted order.
func Names() []string {
	out := make([]string, 0, len(Valid))
	for s := range Valid {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// IsValid reports whether s is a recognised scope.
func IsValid(s string) bool {
	_, ok := Valid[s]
	return ok
}
