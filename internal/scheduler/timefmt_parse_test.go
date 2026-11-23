package scheduler

import (
	"testing"
	"time"
)

func TestParseScheduledHeader_SkillNameWithSeparator(t *testing.T) {
	fire := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	text := FormatScheduledTextWithPrev("", "a | b", fire, fire.Add(-6*time.Hour), time.UTC) + " and then some"

	name, isSkill, ok := ParseScheduledHeader(text)
	if !ok || !isSkill || name != "a | b" {
		t.Errorf("ParseScheduledHeader = (%q, %v, %v), want (\"a | b\", true, true)", name, isSkill, ok)
	}
}

func TestParseScheduledHeader_TriggerLabel(t *testing.T) {
	text := FormatScheduledText("nightly | digest", "", time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC), time.UTC)

	name, isSkill, ok := ParseScheduledHeader(text)
	if !ok || isSkill || name != "nightly | digest" {
		t.Errorf("ParseScheduledHeader = (%q, %v, %v), want (\"nightly | digest\", false, true)", name, isSkill, ok)
	}
}

func TestParseScheduledHeader_NotAHeader(t *testing.T) {
	for _, text := range []string{"write me a poem", "[Scheduled: heartbeat]", "[Scheduled: heartbeat | x | y]"} {
		if name, _, ok := ParseScheduledHeader(text); ok {
			t.Errorf("ParseScheduledHeader(%q) = %q, true; want false", text, name)
		}
	}
}
