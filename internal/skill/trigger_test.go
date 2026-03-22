package skill

import (
	"testing"
)

func TestParseTrigger_Command(t *testing.T) {
	tr, err := ParseTrigger("command:briefing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Type != TriggerCommand {
		t.Errorf("type = %d, want TriggerCommand", tr.Type)
	}
	if tr.Command != "briefing" {
		t.Errorf("command = %q, want %q", tr.Command, "briefing")
	}
	if tr.Raw != "command:briefing" {
		t.Errorf("raw = %q, want %q", tr.Raw, "command:briefing")
	}
}

func TestParseTrigger_CommandUpperCase(t *testing.T) {
	tr, err := ParseTrigger("command:MyCommand")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Command != "mycommand" {
		t.Errorf("command = %q, want %q (lowercased)", tr.Command, "mycommand")
	}
}

func TestParseTrigger_Schedule(t *testing.T) {
	tr, err := ParseTrigger("schedule:daily:08:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.Type != TriggerSchedule {
		t.Errorf("type = %d, want TriggerSchedule", tr.Type)
	}
	if tr.Raw != "schedule:daily:08:00" {
		t.Errorf("raw = %q, want %q", tr.Raw, "schedule:daily:08:00")
	}
}

func TestParseTrigger_InvalidPrefix(t *testing.T) {
	_, err := ParseTrigger("unknown:foo")
	if err == nil {
		t.Fatal("expected error for unknown trigger type")
	}
}

func TestParseTrigger_EmptyString(t *testing.T) {
	_, err := ParseTrigger("")
	if err == nil {
		t.Fatal("expected error for empty trigger")
	}
}

func TestParseTrigger_MissingName(t *testing.T) {
	_, err := ParseTrigger("command:")
	if err == nil {
		t.Fatal("expected error for command with empty name")
	}
}

func TestParseTrigger_NoSeparator(t *testing.T) {
	_, err := ParseTrigger("noseparator")
	if err == nil {
		t.Fatal("expected error for trigger without ':'")
	}
}

func TestParseTriggers_Multiple(t *testing.T) {
	triggers, err := ParseTriggers([]string{"command:help", "schedule:daily:09:00"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(triggers) != 2 {
		t.Fatalf("got %d triggers, want 2", len(triggers))
	}
	if triggers[0].Type != TriggerCommand {
		t.Errorf("first trigger type = %d, want TriggerCommand", triggers[0].Type)
	}
	if triggers[1].Type != TriggerSchedule {
		t.Errorf("second trigger type = %d, want TriggerSchedule", triggers[1].Type)
	}
}

func TestParseTriggers_Empty(t *testing.T) {
	triggers, err := ParseTriggers(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggers != nil {
		t.Errorf("got %v, want nil for empty input", triggers)
	}
}

func TestParseTriggers_InvalidStopsAll(t *testing.T) {
	_, err := ParseTriggers([]string{"command:ok", "bad"})
	if err == nil {
		t.Fatal("expected error when one trigger is invalid")
	}
}
