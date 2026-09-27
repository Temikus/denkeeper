package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/scheduler"
)

// lintSkillServer is skillServer with a scheduler holding one schedule, "weekly-review".
func lintSkillServer(t *testing.T, dir string) *Server {
	t.Helper()
	s, _ := skillServer(t, dir)
	sched := scheduler.New(testLogger(), nil)
	s.deps.Scheduler = sched
	err := sched.Register(scheduler.Config{
		Name:     "weekly-review",
		Type:     string(scheduler.ScheduleTypeAgent),
		Agent:    "test-agent",
		Schedule: "@weekly",
		Skill:    "review",
	}, func(scheduler.Entry) {})
	if err != nil {
		t.Fatalf("registering schedule: %v", err)
	}
	return s
}

func TestSkillCreate_ScheduleTriggerForMissingSchedule_Rejected(t *testing.T) {
	dir := t.TempDir()
	s := lintSkillServer(t, dir)

	res, _, err := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent:    "test-agent",
		Name:     "review",
		Version:  "1.0.0",
		Body:     "Review skills.",
		Triggers: []string{"schedule:weekly-review-sun-9am"},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection, got: %s", toolResultText(res))
	}
	if _, statErr := os.Stat(filepath.Join(dir, "review.md")); !os.IsNotExist(statErr) {
		t.Errorf("rejected skill was written to disk (stat err = %v)", statErr)
	}
}

func TestSkillCreate_ScheduledWithoutRoundCap_Warns(t *testing.T) {
	s := lintSkillServer(t, t.TempDir())

	res, _, err := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent:    "test-agent",
		Name:     "review",
		Version:  "1.0.0",
		Body:     "Review skills.",
		Triggers: []string{"schedule:weekly-review"},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("a missing cap is a warning, not an error: %s", toolResultText(res))
	}
	if !strings.Contains(toolResultText(res), "max_tool_rounds") {
		t.Errorf("expected max_tool_rounds warning: %s", toolResultText(res))
	}
}

func TestSkillUpdate_ScheduleTriggerForMissingSchedule_Rejected(t *testing.T) {
	s := lintSkillServer(t, t.TempDir())
	createSkill(t, s, "review", "Review skills.")

	res, _, err := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{
		Agent:    "test-agent",
		Name:     "review",
		Triggers: []string{"schedule:weekly-review-sun-9am"},
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected rejection, got: %s", toolResultText(res))
	}
}

func TestSkillUpdate_BodyChangedVersionKept_Warns(t *testing.T) {
	s := lintSkillServer(t, t.TempDir())
	createSkill(t, s, "review", "Review skills.")
	body := "Review skills carefully."

	res, _, err := s.handleSkillUpdate(writeScope(t), nil, skillUpdateInput{
		Agent: "test-agent",
		Name:  "review",
		Body:  &body,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("an unchanged version is a warning, not an error: %s", toolResultText(res))
	}
	if !strings.Contains(toolResultText(res), "version") {
		t.Errorf("expected version warning: %s", toolResultText(res))
	}
}

func createSkill(t *testing.T, s *Server, name, body string) {
	t.Helper()
	res, _, err := s.handleSkillCreate(writeScope(t), nil, skillCreateInput{
		Agent: "test-agent", Name: name, Version: "1.0.0", Body: body,
	})
	if err != nil || res.IsError {
		t.Fatalf("creating %q: err=%v result=%s", name, err, toolResultText(res))
	}
}
