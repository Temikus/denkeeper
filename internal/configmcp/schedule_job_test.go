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

	cfg := scheduler.Config{Name: "heartbeat", Skill: "heartbeat", Channel: "telegram:123"}
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
