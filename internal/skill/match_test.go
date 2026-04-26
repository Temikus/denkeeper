package skill

import (
	"testing"
)

func makeSkill(name string, triggers ...string) Skill {
	parsed, _ := ParseTriggers(triggers)
	return Skill{
		Name:           name,
		Description:    name + " description",
		ParsedTriggers: parsed,
		Body:           name + " body",
	}
}

func TestMatchSkills_NoTriggers_AlwaysIncluded(t *testing.T) {
	skills := []Skill{makeSkill("general")}
	matched := MatchSkills(skills, MatchContext{MessageText: "hello"})
	if len(matched) != 1 {
		t.Fatalf("got %d matched, want 1", len(matched))
	}
	if matched[0].Name != "general" {
		t.Errorf("matched name = %q, want %q", matched[0].Name, "general")
	}
}

func TestMatchSkills_CommandTrigger_SlashPrefix(t *testing.T) {
	skills := []Skill{makeSkill("briefing", "command:briefing")}
	matched := MatchSkills(skills, MatchContext{MessageText: "/briefing"})
	if len(matched) != 1 {
		t.Fatalf("got %d matched, want 1", len(matched))
	}
}

func TestMatchSkills_CommandTrigger_BangPrefix(t *testing.T) {
	skills := []Skill{makeSkill("briefing", "command:briefing")}
	matched := MatchSkills(skills, MatchContext{MessageText: "!briefing"})
	if len(matched) != 1 {
		t.Fatalf("got %d matched, want 1", len(matched))
	}
}

func TestMatchSkills_CommandTrigger_CaseInsensitive(t *testing.T) {
	skills := []Skill{makeSkill("briefing", "command:briefing")}
	matched := MatchSkills(skills, MatchContext{MessageText: "/Briefing"})
	if len(matched) != 1 {
		t.Fatalf("got %d matched, want 1", len(matched))
	}
}

func TestMatchSkills_CommandTrigger_NoMatch(t *testing.T) {
	skills := []Skill{makeSkill("briefing", "command:briefing")}
	matched := MatchSkills(skills, MatchContext{MessageText: "/other"})
	if len(matched) != 0 {
		t.Fatalf("got %d matched, want 0", len(matched))
	}
}

func TestMatchSkills_CommandTrigger_WithArgs(t *testing.T) {
	skills := []Skill{makeSkill("briefing", "command:briefing")}
	matched := MatchSkills(skills, MatchContext{MessageText: "/briefing weather news"})
	if len(matched) != 1 {
		t.Fatalf("got %d matched, want 1", len(matched))
	}
}

func TestMatchSkills_CommandTrigger_PlainTextNoMatch(t *testing.T) {
	skills := []Skill{makeSkill("briefing", "command:briefing")}
	matched := MatchSkills(skills, MatchContext{MessageText: "briefing"})
	if len(matched) != 0 {
		t.Fatalf("got %d matched, want 0 (no / or ! prefix)", len(matched))
	}
}

func TestMatchSkills_ScheduleTrigger_MatchByName(t *testing.T) {
	skills := []Skill{makeSkill("daily-briefing", "schedule:daily:08:00")}
	matched := MatchSkills(skills, MatchContext{SkillName: "daily-briefing"})
	if len(matched) != 1 {
		t.Fatalf("got %d matched, want 1", len(matched))
	}
}

func TestMatchSkills_ScheduleTrigger_NoMatch(t *testing.T) {
	skills := []Skill{makeSkill("daily-briefing", "schedule:daily:08:00")}
	matched := MatchSkills(skills, MatchContext{SkillName: "other-skill"})
	if len(matched) != 0 {
		t.Fatalf("got %d matched, want 0", len(matched))
	}
}

func TestMatchSkills_SkillName_OverridesMissingScheduleTrigger(t *testing.T) {
	// A skill with only a command trigger should still match when the
	// scheduler explicitly names it via SkillName. Without this, a misconfigured
	// schedule (skill missing `schedule:` trigger) silently fires with no skill body.
	skills := []Skill{makeSkill("heartbeat", "command:heartbeat")}
	matched := MatchSkills(skills, MatchContext{
		SkillName:   "heartbeat",
		MessageText: "[Scheduled: heartbeat]",
	})
	if len(matched) != 1 {
		t.Fatalf("got %d matched, want 1", len(matched))
	}
	if matched[0].Name != "heartbeat" {
		t.Errorf("matched name = %q, want %q", matched[0].Name, "heartbeat")
	}
}

func TestMatchSkills_MixedSkills(t *testing.T) {
	skills := []Skill{
		makeSkill("always-on"),                              // no triggers — always included
		makeSkill("briefing", "command:briefing"),           // command trigger
		makeSkill("daily-briefing", "schedule:daily:08:00"), // schedule trigger
		makeSkill("expense-tracker", "command:expense"),     // another command
	}

	// Plain message: only always-on
	matched := MatchSkills(skills, MatchContext{MessageText: "hello"})
	if len(matched) != 1 || matched[0].Name != "always-on" {
		t.Errorf("plain message: got %d matched, want 1 (always-on)", len(matched))
	}

	// Command message: always-on + matching command skill
	matched = MatchSkills(skills, MatchContext{MessageText: "/briefing"})
	if len(matched) != 2 {
		t.Fatalf("/briefing: got %d matched, want 2", len(matched))
	}

	// Schedule trigger: always-on + matching schedule skill
	matched = MatchSkills(skills, MatchContext{SkillName: "daily-briefing", MessageText: "[Scheduled: daily-briefing]"})
	if len(matched) != 2 {
		t.Fatalf("schedule: got %d matched, want 2", len(matched))
	}
}

func TestMatchSkills_MultipleCommandTriggers(t *testing.T) {
	skills := []Skill{makeSkill("helper", "command:help", "command:skills")}

	matched := MatchSkills(skills, MatchContext{MessageText: "/help"})
	if len(matched) != 1 {
		t.Fatalf("/help: got %d matched, want 1", len(matched))
	}

	matched = MatchSkills(skills, MatchContext{MessageText: "/skills"})
	if len(matched) != 1 {
		t.Fatalf("/skills: got %d matched, want 1", len(matched))
	}
}

func TestMatchSkills_EmptyMessage(t *testing.T) {
	skills := []Skill{
		makeSkill("always-on"),
		makeSkill("briefing", "command:briefing"),
	}
	matched := MatchSkills(skills, MatchContext{MessageText: ""})
	if len(matched) != 1 || matched[0].Name != "always-on" {
		t.Errorf("empty message: got %d matched, want 1 (always-on only)", len(matched))
	}
}
