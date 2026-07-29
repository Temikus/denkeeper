package skill

import (
	"strings"
)

// MatchContext carries the per-message information needed to decide which skills apply.
type MatchContext struct {
	MessageText string // the user's message
	SkillName   string // set when a schedule fires for a specific skill
}

// MatchSkills returns the subset of skills that should be injected for this message.
// Skills with no triggers are always included. Otherwise, a skill is included if
// any of its triggers match the context.
//
// Matching keys off ParsedTriggers, never the raw Triggers strings. Every Skill
// reaching this function is expected to have come from ParseFile, which fills
// ParsedTriggers whenever Triggers is non-empty (and rejects the skill outright
// if a trigger fails to parse). A hand-built Skill that sets Triggers but leaves
// ParsedTriggers nil therefore looks trigger-less and is injected into *every*
// message — construct such values with skill/skilltest rather than a bare
// literal. See TestMatchSkills_RawTriggersAreNotHonoured.
func MatchSkills(skills []Skill, ctx MatchContext) []Skill {
	var matched []Skill
	for _, s := range skills {
		if len(s.ParsedTriggers) == 0 {
			matched = append(matched, s)
			continue
		}
		if skillMatches(s, ctx) {
			matched = append(matched, s)
		}
	}
	return matched
}

// skillMatches returns true if any of the skill's triggers match the context.
func skillMatches(s Skill, ctx MatchContext) bool {
	// SkillName is a scheduler routing directive — honor it regardless of declared triggers.
	if ctx.SkillName != "" && ctx.SkillName == s.Name {
		return true
	}
	for _, t := range s.ParsedTriggers {
		switch t.Type {
		case TriggerCommand:
			if commandMatches(t.Command, ctx.MessageText) {
				return true
			}
		case TriggerSchedule:
			if ctx.SkillName != "" && ctx.SkillName == s.Name {
				return true
			}
		}
	}
	return false
}

// commandMatches checks if the first token of text is /cmd or !cmd (case-insensitive).
func commandMatches(cmd, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	// Extract first token.
	first := text
	if idx := strings.IndexByte(text, ' '); idx != -1 {
		first = text[:idx]
	}

	if len(first) < 2 {
		return false
	}

	prefix := first[0]
	if prefix != '/' && prefix != '!' {
		return false
	}

	return strings.EqualFold(first[1:], cmd)
}
