package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchedules_HeadlessLoads(t *testing.T) {
	cfg, err := Parse(deciderConfig(`
[[schedules]]
name = "check-tasks"
type = "agent"
schedule = "@every 30m"
headless = true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.Schedules[0].Headless {
		t.Error("headless = true not loaded")
	}
}

func TestSchedules_HeadlessWithChannelRejected(t *testing.T) {
	parseDeciderErr(t, `
[[schedules]]
name = "check-tasks"
type = "agent"
schedule = "@every 30m"
channel = "telegram:1"
headless = true
`, "headless schedule answers in no chat")
}

func TestScheduleWriter_HeadlessRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, deciderConfig(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AddScheduleToConfig(path, "check-tasks", "@every 30m", "", "", "isolated", "", "default", nil, true, true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after write: %v", err)
	}
	if !cfg.Schedules[0].Headless {
		t.Error("headless lost on write")
	}
	if err := UpdateScheduleInConfig(path, "check-tasks", "@every 1h", "", "", "isolated", "", "default", nil, false, true); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "headless = true") {
		t.Errorf("headless lost on update:\n%s", raw)
	}
}
