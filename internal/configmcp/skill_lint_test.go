package configmcp_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skill/skilltest"
)

// newLintServer wires skill_update/skill_patch over an in-memory skill map
// seeded with existing (nil = empty).
func newLintServer(t *testing.T, existing *skill.Skill) (*mcp.ClientSession, *configmcp.Deps) {
	t.Helper()
	var mu sync.RWMutex
	skills := map[string]skill.Skill{}
	if existing != nil {
		skills[existing.Name] = *existing
	}
	return newTestServer(t, func(d *configmcp.Deps) {
		if err := os.MkdirAll(d.AgentSkillsDir, 0o755); err != nil {
			t.Fatalf("creating skills dir: %v", err)
		}
		d.GetSkill = func(name string) (skill.Skill, bool) {
			mu.RLock()
			defer mu.RUnlock()
			s, ok := skills[name]
			return s, ok
		}
		d.UpdateSkill = func(name string, s skill.Skill) bool {
			mu.Lock()
			defer mu.Unlock()
			skills[name] = s
			return true
		}
		d.RemoveSkill = func(name string) bool {
			mu.Lock()
			defer mu.Unlock()
			delete(skills, name)
			return true
		}
	})
}

func registerSchedule(t *testing.T, sched *scheduler.Scheduler, name string) {
	t.Helper()
	err := sched.Register(scheduler.Config{
		Name:     name,
		Type:     string(scheduler.ScheduleTypeAgent),
		Agent:    "test-agent",
		Schedule: "@weekly",
		Skill:    "review",
	}, func(scheduler.Entry) {})
	if err != nil {
		t.Fatalf("registering schedule: %v", err)
	}
}

func TestSkillCreate_ScheduleTriggerForMissingSchedule_Rejected(t *testing.T) {
	session, deps := newLintServer(t, nil)

	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name":     "review",
		"body":     "Review skills.",
		"triggers": []string{"schedule:weekly-review-sun-9am"},
	})
	if !isErr {
		t.Fatalf("expected rejection, got: %s", text)
	}
	if !strings.Contains(text, "weekly-review-sun-9am") {
		t.Errorf("error should name the missing schedule: %s", text)
	}
	if _, err := os.Stat(filepath.Join(deps.AgentSkillsDir, "review.md")); !os.IsNotExist(err) {
		t.Errorf("rejected skill was written to disk (stat err = %v)", err)
	}
}

func TestSkillCreate_ScheduleTriggerForExistingSchedule_Accepted(t *testing.T) {
	session, deps := newLintServer(t, nil)
	registerSchedule(t, deps.Sched, "weekly-review")

	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name":            "review",
		"body":            "Review skills.",
		"triggers":        []string{"schedule:weekly-review"},
		"max_tool_rounds": 10,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if strings.Contains(text, "Warning") {
		t.Errorf("clean skill should carry no warnings: %s", text)
	}
}

// A bare marker names nothing, so there is nothing to drift.
func TestSkillCreate_BareScheduleMarker_Accepted(t *testing.T) {
	session, _ := newLintServer(t, nil)

	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name":            "review",
		"body":            "Review skills.",
		"triggers":        []string{"schedule:"},
		"max_tool_rounds": 10,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
}

func TestSkillCreate_ScheduledWithoutRoundCap_Warns(t *testing.T) {
	session, deps := newLintServer(t, nil)
	registerSchedule(t, deps.Sched, "weekly-review")

	text, isErr := callTool(t, session, "skill_create", map[string]any{
		"name":     "review",
		"body":     "Review skills.",
		"triggers": []string{"schedule:weekly-review"},
	})
	if isErr {
		t.Fatalf("a missing cap is a warning, not an error: %s", text)
	}
	if !strings.Contains(text, "max_tool_rounds") {
		t.Errorf("expected max_tool_rounds warning in result: %s", text)
	}
}

func TestSkillUpdate_NewScheduleTriggerForMissingSchedule_Rejected(t *testing.T) {
	existing := skilltest.NewVersioned("review", "", "1.0.0", []string{"schedule:weekly-review"}, "Review skills.")
	session, deps := newLintServer(t, &existing)
	registerSchedule(t, deps.Sched, "weekly-review")

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name":     "review",
		"triggers": []string{"schedule:weekly-review-sun-9am"},
	})
	if !isErr {
		t.Fatalf("expected rejection, got: %s", text)
	}
}

// Triggers already on disk were written before the check existed; an edit that
// keeps them must not be blocked by them.
func TestSkillUpdate_CarriedOverScheduleTrigger_NotRejected(t *testing.T) {
	existing := skilltest.NewVersioned("review", "", "1.0.0", []string{"schedule:daily:08:00"}, "Review skills.")
	session, _ := newLintServer(t, &existing)

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name":    "review",
		"body":    "Review skills carefully.",
		"version": "1.1.0",
	})
	if isErr {
		t.Fatalf("carried-over trigger should not block the edit: %s", text)
	}
}

func TestSkillUpdate_BodyChangedVersionKept_Warns(t *testing.T) {
	existing := skilltest.NewVersioned("greet", "", "1.0.0", nil, "Say hello.")
	session, _ := newLintServer(t, &existing)

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name": "greet",
		"body": "Say hi.",
	})
	if isErr {
		t.Fatalf("an unchanged version is a warning, not an error: %s", text)
	}
	if !strings.Contains(text, "version") {
		t.Errorf("expected version warning in result: %s", text)
	}
}

func TestSkillUpdate_BodyAndVersionChanged_NoWarning(t *testing.T) {
	existing := skilltest.NewVersioned("greet", "", "1.0.0", nil, "Say hello.")
	session, _ := newLintServer(t, &existing)

	text, isErr := callTool(t, session, "skill_update", map[string]any{
		"name":    "greet",
		"body":    "Say hi.",
		"version": "1.0.1",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if strings.Contains(text, "Warning") {
		t.Errorf("expected no warnings: %s", text)
	}
}

func TestSkillPatch_VersionKept_Warns(t *testing.T) {
	existing := skilltest.NewVersioned("greet", "", "1.0.0", nil, "Say hello.")
	session, _ := newLintServer(t, &existing)

	text, isErr := callTool(t, session, "skill_patch", map[string]any{
		"name":       "greet",
		"old_string": "hello",
		"new_string": "hi",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if !strings.Contains(text, "version") {
		t.Errorf("expected version warning in result: %s", text)
	}
}
