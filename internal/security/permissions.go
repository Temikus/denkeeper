package security

// PermissionEngine controls what actions the agent is allowed to perform.
// Phase 1: "supervised" tier only — allows chat, denies everything else.
type PermissionEngine struct {
	tier      string
	allowlist map[string]bool
}

func NewPermissionEngine() *PermissionEngine {
	return &PermissionEngine{
		tier: "supervised",
		allowlist: map[string]bool{
			"chat":           true,
			"read_memory":    true,
			"write_memory":   true,
		},
	}
}

// CanExecute checks if an action is allowed under the current tier.
func (p *PermissionEngine) CanExecute(action string) bool {
	return p.allowlist[action]
}

// Tier returns the current permission tier.
func (p *PermissionEngine) Tier() string {
	return p.tier
}
