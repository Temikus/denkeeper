//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/scheduler"
)

func TestSchedule_ListEmpty(t *testing.T) {
	h := NewHarness(t, nil)

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/schedules", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var schedules []map[string]any
	DecodeJSON(t, rec, &schedules)
	if len(schedules) != 0 {
		t.Errorf("schedules count = %d, want 0", len(schedules))
	}
}

func TestSchedule_RegisterAndList(t *testing.T) {
	h := NewHarness(t, nil)

	// Register a schedule directly via the scheduler.
	cfg := scheduler.Config{
		Name:     "test-schedule",
		Type:     "agent",
		Schedule: "@every 1h",
		Skill:    "greet",
		Channel:  "telegram:12345",
		Enabled:  true,
	}
	fired := make(chan struct{}, 1)
	err := h.Scheduler.Register(cfg, func(_ scheduler.Entry) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// List via API — should include the registered schedule.
	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/schedules", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var schedules []map[string]any
	DecodeJSON(t, rec, &schedules)
	if len(schedules) != 1 {
		t.Fatalf("schedules count = %d, want 1", len(schedules))
	}
	if schedules[0]["name"] != "test-schedule" {
		t.Errorf("schedule name = %v, want test-schedule", schedules[0]["name"])
	}
}

func TestSchedule_GetEntry(t *testing.T) {
	h := NewHarness(t, nil)

	cfg := scheduler.Config{
		Name:     "lookup-test",
		Type:     "agent",
		Schedule: "@daily",
		Skill:    "help",
		Channel:  "telegram:99999",
		Enabled:  true,
	}
	_ = h.Scheduler.Register(cfg, func(_ scheduler.Entry) {})

	entry, ok := h.Scheduler.GetEntry("lookup-test")
	if !ok {
		t.Fatal("expected GetEntry to find the schedule")
	}
	if entry.Name != "lookup-test" {
		t.Errorf("entry.Name = %q, want lookup-test", entry.Name)
	}
	if entry.Skill != "help" {
		t.Errorf("entry.Skill = %q, want help", entry.Skill)
	}
}

func TestSchedule_UnregisterAndVerify(t *testing.T) {
	h := NewHarness(t, nil)

	cfg := scheduler.Config{
		Name:     "remove-me",
		Type:     "agent",
		Schedule: "@hourly",
		Channel:  "telegram:1",
		Enabled:  true,
	}
	_ = h.Scheduler.Register(cfg, func(_ scheduler.Entry) {})

	// Verify it exists.
	if _, ok := h.Scheduler.GetEntry("remove-me"); !ok {
		t.Fatal("schedule should exist before unregister")
	}

	// Unregister.
	if err := h.Scheduler.Unregister("remove-me"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	// Verify it's gone.
	if _, ok := h.Scheduler.GetEntry("remove-me"); ok {
		t.Error("schedule should not exist after unregister")
	}
}
