package configmcp

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

func TestBuildScheduleJob_TextIncludesDateHeader(t *testing.T) {
	sydney, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Fatalf("loading Australia/Sydney: %v", err)
	}

	var captured adapter.IncomingMessage
	handleMsg := func(_ context.Context, msg adapter.IncomingMessage) error {
		captured = msg
		return nil
	}

	cfg := scheduler.Config{Name: "heartbeat", Schedule: "30 8 * * *", Skill: "heartbeat", Channel: "telegram:123"}
	job := BuildScheduleJob(cfg, handleMsg, slog.Default(), nil, BuildScheduleJobOpts{Location: sydney})

	before := time.Now()
	job(scheduler.Entry{Name: "heartbeat", Skill: "heartbeat"})

	if !strings.HasPrefix(captured.Text, "[Scheduled: heartbeat | ") {
		t.Errorf("Text = %q, want prefix %q", captured.Text, "[Scheduled: heartbeat | ")
	}
	if !strings.Contains(captured.Text, "Australia/Sydney") {
		t.Errorf("Text = %q, should render the configured timezone", captured.Text)
	}
	if !regexp.MustCompile(`\| \d{4}-W\d{2}\]$`).MatchString(captured.Text) {
		t.Errorf("Text = %q, should end with an ISO-week suffix", captured.Text)
	}
	if captured.Timestamp.Before(before) {
		t.Errorf("Timestamp = %v, should be set to the fire time (>= %v)", captured.Timestamp, before)
	}
	if !captured.IsScheduled {
		t.Error("IsScheduled should be true — gates audit trigger_source and supervisor eval framing")
	}
	if captured.ScheduleName != "heartbeat" || captured.ScheduleCron != "30 8 * * *" {
		t.Errorf("schedule metadata not propagated: name=%q cron=%q", captured.ScheduleName, captured.ScheduleCron)
	}
}

func TestBuildScheduleJob_NilLocationDefaultsUTC(t *testing.T) {
	var captured adapter.IncomingMessage
	handleMsg := func(_ context.Context, msg adapter.IncomingMessage) error {
		captured = msg
		return nil
	}

	cfg := scheduler.Config{Name: "nightly-ping", Channel: "telegram:123"}
	job := BuildScheduleJob(cfg, handleMsg, slog.Default(), nil, BuildScheduleJobOpts{})

	job(scheduler.Entry{Name: "nightly-ping"})

	if !strings.HasPrefix(captured.Text, "[Scheduled trigger: nightly-ping | ") {
		t.Errorf("Text = %q, want trigger-label prefix", captured.Text)
	}
	if !strings.Contains(captured.Text, " UTC | ") {
		t.Errorf("Text = %q, should fall back to UTC", captured.Text)
	}
}

func TestBuildScheduleJob_HeadlessRunsOnceWithNoTarget(t *testing.T) {
	var got []adapter.IncomingMessage
	handleMsg := func(_ context.Context, msg adapter.IncomingMessage) error {
		got = append(got, msg)
		return nil
	}
	cfg := scheduler.Config{Name: "check-tasks-pamela", Schedule: "@every 30m", Skill: "check-tasks", Headless: true}
	job := BuildScheduleJob(cfg, handleMsg, slog.Default(), nil, BuildScheduleJobOpts{})
	job(scheduler.Entry{Name: "check-tasks-pamela", Skill: "check-tasks"})

	if len(got) != 1 {
		t.Fatalf("handled %d messages, want 1", len(got))
	}
	m := got[0]
	if !m.Headless || m.Adapter != "" || m.ExternalID != "" {
		t.Errorf("message = headless %v, target %q:%q; want headless with no target", m.Headless, m.Adapter, m.ExternalID)
	}
	if m.ConversationID != "sched:check-tasks-pamela" {
		t.Errorf("ConversationID = %q, want sched:check-tasks-pamela", m.ConversationID)
	}
}

func TestMergeScheduleUpdate_KeepsHeadless(t *testing.T) {
	existing := scheduler.Entry{Name: "check-tasks-pamela", Expr: "@every 30m", Skill: "check-tasks", Agent: "pamela", Headless: true, Enabled: true}
	paused := false
	cfg, errMsg := MergeScheduleUpdate(existing, ScheduleUpdateInput{Name: existing.Name, Enabled: &paused})
	if errMsg != "" {
		t.Fatalf("merge refused an edit to a headless schedule: %s", errMsg)
	}
	if !cfg.Headless || cfg.Channel != "" || cfg.Enabled {
		t.Errorf("merged = %+v, want headless kept, no channel, disabled", cfg)
	}

	ch := "telegram:1"
	if _, errMsg := MergeScheduleUpdate(existing, ScheduleUpdateInput{Name: existing.Name, Channel: &ch}); errMsg == "" {
		t.Error("a channel on a headless schedule was accepted")
	}
	off := false
	cfg, errMsg = MergeScheduleUpdate(existing, ScheduleUpdateInput{Name: existing.Name, Channel: &ch, Headless: &off})
	if errMsg != "" || cfg.Headless || cfg.Channel != ch {
		t.Errorf("switching to a channel = %+v, %q", cfg, errMsg)
	}
}

func TestValidateScheduleDelivery(t *testing.T) {
	for _, tc := range []struct {
		channel  string
		headless bool
		ok       bool
	}{
		{"", true, true},
		{"telegram:1", true, false},
		{"", false, false},
		{"telegram:1", false, true},
		{"@main", false, true},
		{"telegram", false, false},
	} {
		if got := ValidateScheduleDelivery(tc.channel, tc.headless) == ""; got != tc.ok {
			t.Errorf("ValidateScheduleDelivery(%q, %v) ok = %v, want %v", tc.channel, tc.headless, got, tc.ok)
		}
	}
}
