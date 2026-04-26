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
