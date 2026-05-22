package mcpserver

import "testing"

func TestValidateScheduleCreateInput_Valid(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "daily-check",
		Schedule: "0 9 * * *",
		Channel:  "telegram:12345",
	}
	if msg := validateScheduleCreateInput(input); msg != "" {
		t.Errorf("unexpected error: %s", msg)
	}
}

func TestValidateScheduleCreateInput_ChannelRef(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "daily-check",
		Schedule: "0 9 * * *",
		Channel:  "@general",
	}
	if msg := validateScheduleCreateInput(input); msg != "" {
		t.Errorf("unexpected error: %s", msg)
	}
}

func TestValidateScheduleCreateInput_MissingName(t *testing.T) {
	input := scheduleCreateInput{
		Schedule: "0 9 * * *",
		Channel:  "telegram:12345",
	}
	if msg := validateScheduleCreateInput(input); msg != "name is required" {
		t.Errorf("expected 'name is required', got %q", msg)
	}
}

func TestValidateScheduleCreateInput_MissingSchedule(t *testing.T) {
	input := scheduleCreateInput{
		Name:    "test",
		Channel: "telegram:12345",
	}
	if msg := validateScheduleCreateInput(input); msg != "schedule is required" {
		t.Errorf("expected 'schedule is required', got %q", msg)
	}
}

func TestValidateScheduleCreateInput_MissingChannel(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "test",
		Schedule: "0 9 * * *",
	}
	if msg := validateScheduleCreateInput(input); msg != "channel is required" {
		t.Errorf("expected 'channel is required', got %q", msg)
	}
}

func TestValidateScheduleCreateInput_InvalidCron(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "test",
		Schedule: "not a cron",
		Channel:  "telegram:12345",
	}
	msg := validateScheduleCreateInput(input)
	if msg == "" {
		t.Error("expected error for invalid cron")
	}
}

func TestValidateScheduleCreateInput_InvalidChannel(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "test",
		Schedule: "0 9 * * *",
		Channel:  "no-colon",
	}
	msg := validateScheduleCreateInput(input)
	if msg == "" {
		t.Error("expected error for invalid channel format")
	}
}

func TestValidateScheduleCreateInput_WhitespaceOnly(t *testing.T) {
	input := scheduleCreateInput{
		Name:     "  ",
		Schedule: "0 9 * * *",
		Channel:  "telegram:12345",
	}
	if msg := validateScheduleCreateInput(input); msg != "name is required" {
		t.Errorf("expected 'name is required' for whitespace, got %q", msg)
	}
}
