package skill

// MergeSkills combines global and agent-specific skills. Agent-specific skills
// override global skills with the same name. Skills without a matching name in
// the other set are included as-is.
func MergeSkills(global, agentSpecific []Skill) []Skill {
	if len(agentSpecific) == 0 {
		return global
	}
	if len(global) == 0 {
		return agentSpecific
	}

	// Index agent-specific skills by name for O(1) lookup.
	overrides := make(map[string]struct{}, len(agentSpecific))
	for _, s := range agentSpecific {
		overrides[s.Name] = struct{}{}
	}

	// Start with global skills, skipping those overridden by agent-specific.
	merged := make([]Skill, 0, len(global)+len(agentSpecific))
	for _, s := range global {
		if _, ok := overrides[s.Name]; !ok {
			merged = append(merged, s)
		}
	}

	// Append all agent-specific skills.
	merged = append(merged, agentSpecific...)
	return merged
}
