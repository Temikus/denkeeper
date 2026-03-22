package skill

import (
	"fmt"
	"strings"
)

// TriggerType classifies how a skill is activated.
type TriggerType int

const (
	// TriggerCommand activates on a user command like "/briefing" or "!briefing".
	TriggerCommand TriggerType = iota
	// TriggerSchedule activates when the scheduler fires for the matching skill.
	TriggerSchedule
)

// Trigger is a parsed trigger specification from a skill's frontmatter.
type Trigger struct {
	Type    TriggerType
	Raw     string // original string, e.g. "command:briefing"
	Command string // lowercase command name, for TriggerCommand only
}

// ParseTrigger parses a raw trigger string like "command:briefing" into a Trigger.
func ParseTrigger(raw string) (Trigger, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Trigger{}, fmt.Errorf("trigger: empty string")
	}

	idx := strings.Index(raw, ":")
	if idx == -1 {
		return Trigger{}, fmt.Errorf("trigger %q: missing ':' separator", raw)
	}

	prefix := raw[:idx]
	value := raw[idx+1:]

	switch prefix {
	case "command":
		name := strings.TrimSpace(value)
		if name == "" {
			return Trigger{}, fmt.Errorf("trigger %q: missing command name", raw)
		}
		return Trigger{
			Type:    TriggerCommand,
			Raw:     raw,
			Command: strings.ToLower(name),
		}, nil
	case "schedule":
		return Trigger{
			Type: TriggerSchedule,
			Raw:  raw,
		}, nil
	default:
		return Trigger{}, fmt.Errorf("trigger %q: unknown type %q (expected command or schedule)", raw, prefix)
	}
}

// ParseTriggers parses a slice of raw trigger strings. Returns an error if any
// trigger is invalid.
func ParseTriggers(raw []string) ([]Trigger, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	triggers := make([]Trigger, 0, len(raw))
	for _, r := range raw {
		t, err := ParseTrigger(r)
		if err != nil {
			return nil, err
		}
		triggers = append(triggers, t)
	}
	return triggers, nil
}
