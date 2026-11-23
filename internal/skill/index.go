package skill

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Bounds for the skill index. Names, descriptions, commands and schedule
// names are agent-writable, so every one is collapsed to one line. Only
// descriptions are cut: a cut name or command could not be looked up or typed.
// MaxIndexBytes still bounds the section, since it drops whole lines.
const (
	MaxIndexBytes        = 8000
	maxIndexDescRunes    = 120
	maxIndexCommands     = 4
	maxIndexScheduleRefs = 4
)

const indexHeader = "## Available Skills\n\n" +
	"Your skills are listed here, one per line, as: name (how it is triggered): description. " +
	"A skill's full instructions are in this prompt only on a turn that triggers it. " +
	"To describe any other skill in detail, read it with skill_get first. " +
	"To run a skill, the user sends its command; a skill without one runs on its schedule or on every turn. " +
	"Never say a skill does not exist while it is listed here."

const indexNoDescTrailer = "Descriptions are left out to keep this list short. skill_get returns them."

// BuildIndexSection renders every skill as one line: name, triggers and the
// first line of its description. schedules maps a skill name to the names of
// the enabled schedules that fire it.
//
// The output depends only on its inputs, never on their order, so it is
// byte-identical between turns (it sits in the prompt-cache prefix). It never
// exceeds MaxIndexBytes: descriptions are dropped first, then trailing lines.
// Returns "" for no skills.
func BuildIndexSection(skills []Skill, schedules map[string][]string) string {
	if len(skills) == 0 {
		return ""
	}
	sorted := slices.Clone(skills)
	slices.SortStableFunc(sorted, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })

	full := make([]string, len(sorted))
	short := make([]string, len(sorted))
	for i, s := range sorted {
		full[i] = indexLine(s, schedules[s.Name], true)
		short[i] = indexLine(s, schedules[s.Name], false)
	}

	if out := renderIndex(full, ""); len(out) <= MaxIndexBytes {
		return out
	}
	if out := renderIndex(short, indexNoDescTrailer); len(out) <= MaxIndexBytes {
		return out
	}
	return truncatedIndex(short)
}

// truncatedIndex keeps, in sort order, each line that still fits in
// MaxIndexBytes with a trailer naming the dropped count. A line that does not
// fit is skipped, not a stop, so one oversized entry hides only itself. The
// trailer is sized for the most lines that could still be dropped.
func truncatedIndex(lines []string) string {
	size := len(indexHeader) + len("\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		next := size + len("\n") + len(l)
		if next+len("\n\n")+len(droppedTrailer(len(lines)-len(kept)-1)) > MaxIndexBytes {
			continue
		}
		size = next
		kept = append(kept, l)
	}
	return renderIndex(kept, droppedTrailer(len(lines)-len(kept)))
}

func droppedTrailer(n int) string {
	noun, verb := "skills", "are"
	if n == 1 {
		noun, verb = "skill", "is"
	}
	return fmt.Sprintf("Descriptions are left out and %d more %s %s not shown, to keep this list short. "+
		"Call skill_list before saying a skill does not exist.", n, noun, verb)
}

func renderIndex(lines []string, trailer string) string {
	var b strings.Builder
	b.WriteString(indexHeader)
	b.WriteString("\n")
	for _, l := range lines {
		b.WriteString("\n")
		b.WriteString(l)
	}
	if trailer != "" {
		b.WriteString("\n\n")
		b.WriteString(trailer)
	}
	return b.String()
}

// indexLine renders "- name (triggers)" plus ": description" for a skill with
// triggers. An ambient skill's description is already in the prompt every turn.
func indexLine(s Skill, scheduleNames []string, withDesc bool) string {
	line := "- " + collapse(s.Name) + " (" + indexTriggers(s, scheduleNames) + ")"
	if !withDesc || len(s.ParsedTriggers) == 0 {
		return line
	}
	if d := firstLine(s.Description); d != "" {
		line += ": " + oneLine(d, maxIndexDescRunes)
	}
	return line
}

func indexTriggers(s Skill, scheduleNames []string) string {
	var parts []string
	if len(s.ParsedTriggers) == 0 {
		parts = append(parts, "always on")
	}
	var cmds []string
	for _, t := range s.ParsedTriggers {
		if t.Type != TriggerCommand {
			continue
		}
		c := "/" + collapse(t.Command)
		if !slices.Contains(cmds, c) {
			cmds = append(cmds, c)
		}
	}
	if len(cmds) > 0 {
		parts = append(parts, capList(cmds, maxIndexCommands))
	}
	if len(scheduleNames) > 0 {
		names := make([]string, len(scheduleNames))
		for i, n := range scheduleNames {
			names[i] = collapse(n)
		}
		slices.Sort(names)
		parts = append(parts, "schedule: "+capList(names, maxIndexScheduleRefs))
	}
	if len(parts) == 0 {
		return "no active trigger"
	}
	return strings.Join(parts, "; ")
}

func capList(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:limit], ", ") + fmt.Sprintf(", +%d more", len(items)-limit)
}

func firstLine(s string) string {
	for l := range strings.Lines(s) {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// collapse turns whitespace runs, line breaks included, into one space, so an
// agent-written value cannot start a new line or heading.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// oneLine collapses s and cuts it to limit runes plus an ellipsis.
func oneLine(s string, limit int) string {
	s = collapse(s)
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit]) + "…"
}
