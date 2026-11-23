package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
)

const briefingBody = "BRIEFING-BODY-MARKER"

func briefingSkill() skill.Skill {
	return skilltest.New("briefing", "Compile and deliver a daily briefing", []string{"command:briefing"}, briefingBody)
}

// indexOf returns the "## Available Skills" section of a prompt, up to the
// next "## " heading, or "" when absent.
func indexOf(prompt string) string {
	start := strings.Index(prompt, "## Available Skills")
	if start < 0 {
		return ""
	}
	rest := prompt[start:]
	if end := strings.Index(rest[1:], "\n\n## "); end >= 0 {
		return rest[:end+1]
	}
	return rest
}

func promptFor(e *Engine, text string) string {
	return e.buildSystemPrompt(nil, satisfactionMessage("index", text), nil).prompt
}

func registerSchedule(t *testing.T, s *scheduler.Scheduler, name, skillName, agentName string, enabled bool) {
	t.Helper()
	err := s.Register(scheduler.Config{
		Name: name, Type: "agent", Schedule: "@daily",
		Skill: skillName, Agent: agentName, Enabled: enabled,
	}, func(scheduler.Entry) {})
	if err != nil {
		t.Fatalf("Register(%s): %v", name, err)
	}
}

func TestSkillIndex_QuestionAboutCommandShowsLineNotBody(t *testing.T) {
	e := newSatisfactionEngine(t, nil, []skill.Skill{briefingSkill()}, nil)
	prompt := promptFor(e, "what does /briefing do?")

	if !strings.Contains(prompt, "\n- briefing (/briefing): Compile and deliver a daily briefing") {
		t.Fatalf("index line missing:\n%s", prompt)
	}
	if strings.Contains(prompt, briefingBody) {
		t.Fatal("unmatched skill body injected")
	}
}

func TestSkillIndex_Position(t *testing.T) {
	ts := startGuidanceMCPServer(t, "todoist", "find-tasks")
	mgr := newGuidanceToolManager(t)
	registerGuidanceServer(t, mgr, "todoist", ts.URL, "Be careful.")
	e := newSatisfactionEngine(t, mgr, []skill.Skill{briefingSkill()}, nil)
	prompt := promptFor(e, "hi")

	idx := strings.Index(prompt, "## Available Skills")
	if idx < 0 {
		t.Fatalf("no index section:\n%s", prompt)
	}
	for _, before := range []string{"## Structured Memory (KV)", "## Tool Server Notes"} {
		if i := strings.Index(prompt, before); i < 0 || i > idx {
			t.Errorf("%s must come before the index", before)
		}
	}
	for _, after := range []string{"## Session Context", "## Current Date"} {
		if i := strings.Index(prompt, after); i < idx {
			t.Errorf("%s must come after the index", after)
		}
	}
}

func TestSkillIndex_SameTextWhetherMatchedOrNot(t *testing.T) {
	e := newSatisfactionEngine(t, nil, []skill.Skill{briefingSkill()}, nil)
	matched := indexOf(promptFor(e, "/briefing now"))
	unmatched := indexOf(promptFor(e, "hello there"))
	if matched == "" || matched != unmatched {
		t.Fatalf("index varies with the message:\n%q\n---\n%q", matched, unmatched)
	}
}

func TestSkillIndex_MatchedCommandHasLineAndBody(t *testing.T) {
	e := newSatisfactionEngine(t, nil, []skill.Skill{briefingSkill()}, nil)
	prompt := promptFor(e, "/briefing")

	if !strings.Contains(indexOf(prompt), "- briefing (/briefing)") {
		t.Fatalf("index line missing:\n%s", prompt)
	}
	if i := strings.Index(prompt, "# Skills"); i < 0 || !strings.Contains(prompt[i:], briefingBody) {
		t.Fatalf("matched body missing under # Skills:\n%s", prompt)
	}
}

func TestSkillIndex_NoSkillsAddsNothing(t *testing.T) {
	e := newSatisfactionEngine(t, nil, nil, nil)
	on := promptFor(e, "hi")
	e.SetSkillIndex(false)
	off := promptFor(e, "hi")
	if on != off {
		t.Fatalf("index changed the prompt of an agent with no skills:\n%s\n---\n%s", on, off)
	}
}

func TestSkillIndex_DisabledKeepsMatchedBody(t *testing.T) {
	e := newSatisfactionEngine(t, nil, []skill.Skill{briefingSkill()}, nil)
	if !e.SkillIndexEnabled() {
		t.Fatal("index must be on by default")
	}
	e.SetSkillIndex(false)
	if e.SkillIndexEnabled() {
		t.Fatal("SetSkillIndex(false) did not turn it off")
	}
	prompt := promptFor(e, "/briefing")
	if strings.Contains(prompt, "## Available Skills") {
		t.Fatal("index rendered while off")
	}
	if !strings.Contains(prompt, briefingBody) {
		t.Fatal("matched body missing while the index is off")
	}
}

func TestSkillIndex_ListsOnlyThisAgentsEnabledSkillSchedules(t *testing.T) {
	sched := scheduler.New(testLogger(), nil)
	registerSchedule(t, sched, "morning", "briefing", "default", true)
	registerSchedule(t, sched, "other-agent", "briefing", "someone-else", true)
	registerSchedule(t, sched, "paused", "briefing", "default", false)
	registerSchedule(t, sched, "no-skill", "", "default", true)

	e := newSatisfactionEngine(t, nil, []skill.Skill{briefingSkill()}, nil)
	e.SetScheduler(sched)
	idx := indexOf(promptFor(e, "hi"))

	if !strings.Contains(idx, "- briefing (/briefing; schedule: morning):") {
		t.Fatalf("schedule part wrong:\n%s", idx)
	}
	for _, name := range []string{"other-agent", "paused", "no-skill"} {
		if strings.Contains(idx, name) {
			t.Errorf("schedule %q listed", name)
		}
	}
}

func TestSkillIndex_NoSchedulerNoSchedulePart(t *testing.T) {
	e := newSatisfactionEngine(t, nil, []skill.Skill{briefingSkill()}, nil)
	idx := indexOf(promptFor(e, "hi"))
	if idx == "" || strings.Contains(idx, "schedule:") {
		t.Fatalf("unexpected index without a scheduler:\n%s", idx)
	}
}

func TestSkillIndex_GatedSkillListedButNotInjected(t *testing.T) {
	s := skilltest.NewWithRequiresTools("inbox-triage", "Triage the inbox", nil, "TRIAGE-BODY", []string{"gmail_list"})
	e := newSatisfactionEngine(t, newSatisfactionToolManager(t), []skill.Skill{s}, nil)
	prompt := promptFor(e, "hi")

	if strings.Contains(prompt, "TRIAGE-BODY") {
		t.Fatal("gated skill body injected")
	}
	if !strings.Contains(indexOf(prompt), "- inbox-triage (always on)") {
		t.Fatalf("gated skill missing from the index:\n%s", prompt)
	}
}

func TestSkillIndex_AppendSkillAddsLineThenStable(t *testing.T) {
	e := newSatisfactionEngine(t, nil, []skill.Skill{briefingSkill()}, nil)
	before := indexOf(promptFor(e, "hi"))
	e.AppendSkill(skilltest.New("archive", "Archive old notes", []string{"command:archive"}, "ARCHIVE-BODY"))
	first := indexOf(promptFor(e, "hi"))
	second := indexOf(promptFor(e, "something else"))

	if first == before || !strings.Contains(first, "- archive (/archive): Archive old notes") {
		t.Fatalf("appended skill not listed:\n%s", first)
	}
	if first != second {
		t.Fatalf("index not stable after append:\n%q\n---\n%q", first, second)
	}
}

func TestSkillIndex_DryRunPolicySameText(t *testing.T) {
	e := newSatisfactionEngine(t, nil, []skill.Skill{briefingSkill()}, nil)
	msg := satisfactionMessage("index", "hi")
	live := indexOf(e.buildSystemPrompt(nil, msg, nil).prompt)
	policy := &ExecPolicy{Kind: ExecDryRun, ConvID: "dryrun:x", AsOf: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	dry := indexOf(e.buildSystemPrompt(nil, msg, policy).prompt)
	if live == "" || live != dry {
		t.Fatalf("dry-run index differs:\n%q\n---\n%q", live, dry)
	}
}
