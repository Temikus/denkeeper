package configmcp

import (
	"fmt"
	"strings"

	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/skill"
)

// ScheduleLookup is the part of the scheduler LintSkillWrite needs.
type ScheduleLookup interface {
	GetEntry(name string) (scheduler.Entry, bool)
}

// LintSkillWrite checks a skill payload before any surface writes it. prior is
// the skill being replaced (nil on create). A non-nil error rejects the write;
// warnings are for the caller's result. A payload that doesn't parse is left
// for the write path to reject, so its error message is unchanged. A nil sched
// skips the schedule check; pass an untyped nil, not a nil *Scheduler.
func LintSkillWrite(sched ScheduleLookup, prior *skill.Skill, payload string) ([]string, error) {
	next, err := skill.ParseFile("(payload)", []byte(payload))
	if err != nil {
		return nil, nil
	}
	if err := checkScheduleTriggers(sched, prior, next); err != nil {
		return nil, err
	}

	var warnings []string
	if prior != nil && next.Body != prior.Body && next.Version == prior.Version {
		warnings = append(warnings, fmt.Sprintf("body changed but version is still %q; bump it so telemetry can tell the revisions apart", next.Version))
	}
	if next.MaxToolRounds == 0 && hasScheduleTrigger(next) {
		warnings = append(warnings, "scheduled skill has no max_tool_rounds; unattended runs are bounded only by the agent's round limit")
	}
	return warnings, nil
}

// checkScheduleTriggers rejects a schedule trigger naming a schedule that
// doesn't exist. Triggers carried over unchanged from prior are exempt: they
// predate the check, and blocking them would block every edit to the skill.
func checkScheduleTriggers(sched ScheduleLookup, prior *skill.Skill, next *skill.Skill) error {
	if sched == nil {
		return nil
	}
	kept := map[string]bool{}
	if prior != nil {
		for _, raw := range prior.Triggers {
			kept[strings.TrimSpace(raw)] = true
		}
	}
	var missing []string
	for _, t := range next.ParsedTriggers {
		if t.Type != skill.TriggerSchedule || t.Schedule == "" || kept[t.Raw] {
			continue
		}
		if _, ok := sched.GetEntry(t.Schedule); !ok {
			missing = append(missing, fmt.Sprintf("%q", t.Schedule))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("schedule trigger names a schedule that does not exist: %s (create the schedule first, or use a bare \"schedule:\" marker)", strings.Join(missing, ", "))
	}
	return nil
}

func hasScheduleTrigger(s *skill.Skill) bool {
	for _, t := range s.ParsedTriggers {
		if t.Type == skill.TriggerSchedule {
			return true
		}
	}
	return false
}

// FormatSkillWarnings renders warnings as a suffix for a success message.
func FormatSkillWarnings(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	return "\nWarnings:\n- " + strings.Join(warnings, "\n- ")
}
