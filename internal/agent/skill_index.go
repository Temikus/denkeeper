package agent

import (
	"slices"

	"github.com/Temikus/denkeeper/internal/skill"
)

// SetSkillIndex turns the "## Available Skills" prompt section on or off.
// It is on unless switched off.
func (e *Engine) SetSkillIndex(enabled bool) {
	e.skillIndexOff.Store(!enabled)
}

// SkillIndexEnabled reports whether the prompt lists every skill.
func (e *Engine) SkillIndexEnabled() bool {
	return !e.skillIndexOff.Load()
}

// skillSchedules maps each skill to the sorted names of this agent's enabled
// schedules that fire it. Nil without a scheduler.
func (e *Engine) skillSchedules() map[string][]string {
	if e.sched == nil {
		return nil
	}
	out := map[string][]string{}
	for _, entry := range e.sched.Entries() {
		if !entry.Enabled || entry.Skill == "" || entry.Agent != e.name {
			continue
		}
		out[entry.Skill] = append(out[entry.Skill], entry.Name)
	}
	for _, names := range out {
		slices.Sort(names)
	}
	return out
}

// buildSkillIndexSection lists every skill in the prompt, matched or not. It
// must not read the message, the match result, the tool registry or the clock:
// the text sits in the prompt-cache prefix and has to repeat byte for byte.
func (e *Engine) buildSkillIndexSection() string {
	if !e.SkillIndexEnabled() {
		return ""
	}
	schedules := e.skillSchedules()
	// Render under the lock: UpdateSkill writes elements in place.
	e.skillsMu.RLock()
	section := skill.BuildIndexSection(e.skills, schedules)
	e.skillsMu.RUnlock()
	if section == "" {
		return ""
	}
	return "\n\n" + section
}
