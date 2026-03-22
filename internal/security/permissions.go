package security

import "fmt"

// PermissionEngine controls what actions the agent is allowed to perform.
// The allowlist is determined by the configured permission tier.
type PermissionEngine struct {
	tier      string
	allowlist map[string]bool
}

// tierAllowlists defines the permitted actions for each tier.
var tierAllowlists = map[string][]string{
	"autonomous": {
		"chat", "read_memory", "write_memory",
		"read_user_file", "write_user_file",
		"use_tools", "use_read_only_tools",
		"create_skill", "modify_schedule", "change_fallback",
		"execute_shell", "access_filesystem",
	},
	"supervised": {
		"chat", "read_memory", "write_memory",
		"read_user_file", "write_user_file",
		"use_tools", "use_read_only_tools",
	},
	"restricted": {
		"chat", "read_memory", "write_memory",
		"use_read_only_tools",
	},
}

// NewPermissionEngine creates a permission engine for the given tier.
// Valid tiers: "autonomous", "supervised", "restricted".
func NewPermissionEngine(tier string) (*PermissionEngine, error) {
	actions, ok := tierAllowlists[tier]
	if !ok {
		return nil, fmt.Errorf("invalid permission tier %q: must be one of: autonomous, supervised, restricted", tier)
	}
	allowlist := make(map[string]bool, len(actions))
	for _, a := range actions {
		allowlist[a] = true
	}
	return &PermissionEngine{tier: tier, allowlist: allowlist}, nil
}

// NewDenyAll creates a permission engine that denies every action.
// Intended for testing scenarios where no permissions should be granted.
func NewDenyAll() *PermissionEngine {
	return &PermissionEngine{tier: "none", allowlist: nil}
}

// CanExecute checks if an action is allowed under the current tier.
func (p *PermissionEngine) CanExecute(action string) bool {
	return p.allowlist[action]
}

// Tier returns the current permission tier.
func (p *PermissionEngine) Tier() string {
	return p.tier
}

// ValidTier reports whether tier is a recognised permission tier name.
func ValidTier(tier string) bool {
	_, ok := tierAllowlists[tier]
	return ok
}
