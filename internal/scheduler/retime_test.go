package scheduler

import (
	"testing"
	"time"
)

func TestRetimeScheduledText_NoLastRun(t *testing.T) {
	sydney := mustLoadSydney(t)
	in := FormatScheduledText("heartbeat", "heartbeat", time.Date(2026, 7, 7, 10, 45, 0, 0, sydney), sydney)
	// Monday 2026-08-03 09:00 UTC is 19:00 in Sydney, ISO week 32.
	asOf := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)

	got, ok := RetimeScheduledText(in, asOf)
	want := "[Scheduled: heartbeat | 2026-08-03T19:00:00+10:00 Australia/Sydney | 2026-W32]"
	if !ok || got != want {
		t.Errorf("got %q, %v; want %q, true", got, ok, want)
	}
}

func TestRetimeScheduledText_LastRunKeepsGap(t *testing.T) {
	fire := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	in := FormatScheduledTextWithPrev("", "digest", fire, fire.Add(-6*time.Hour), time.UTC)
	asOf := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

	got, ok := RetimeScheduledText(in, asOf)
	want := "[Scheduled: digest | 2026-09-01T08:00:00Z UTC | 2026-W36 | last run 2026-09-01T02:00:00Z (6h ago)]"
	if !ok || got != want {
		t.Errorf("got %q, %v; want %q, true", got, ok, want)
	}
}

func TestRetimeScheduledText_TriggerLabelKept(t *testing.T) {
	in := FormatScheduledText("nightly-ping", "", time.Date(2026, 7, 7, 0, 45, 0, 0, time.UTC), time.UTC)
	asOf := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)

	got, ok := RetimeScheduledText(in, asOf)
	want := "[Scheduled trigger: nightly-ping | 2026-07-10T00:00:00Z UTC | 2026-W28]"
	if !ok || got != want {
		t.Errorf("got %q, %v; want %q, true", got, ok, want)
	}
}

func TestRetimeScheduledText_RoundTripIsIdentity(t *testing.T) {
	sydney := mustLoadSydney(t)
	fire := time.Date(2026, 7, 7, 10, 45, 0, 0, sydney)
	in := FormatScheduledTextWithPrev("hb", "heartbeat", fire, fire.Add(-3*time.Hour), sydney)

	got, ok := RetimeScheduledText(in, fire)
	if !ok || got != in {
		t.Errorf("got %q, %v; want %q, true", got, ok, in)
	}
}

func TestRetimeScheduledText_CrossesDST(t *testing.T) {
	// Sydney leaves standard time (+10:00) for daylight time (+11:00) on
	// 2026-10-04.
	sydney := mustLoadSydney(t)
	in := FormatScheduledText("hb", "heartbeat", time.Date(2026, 9, 1, 9, 0, 0, 0, sydney), sydney)
	asOf := time.Date(2026, 10, 20, 9, 0, 0, 0, sydney)

	got, ok := RetimeScheduledText(in, asOf)
	want := "[Scheduled: heartbeat | 2026-10-20T09:00:00+11:00 Australia/Sydney | 2026-W43]"
	if !ok || got != want {
		t.Errorf("got %q, %v; want %q, true", got, ok, want)
	}
}

func TestRetimeScheduledText_KeepsTextAfterHeader(t *testing.T) {
	in := FormatScheduledText("", "digest", time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC), time.UTC) +
		"\n\nAlso check [the inbox] | twice."
	asOf := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	got, ok := RetimeScheduledText(in, asOf)
	want := "[Scheduled: digest | 2026-07-08T12:00:00Z UTC | 2026-W28]\n\nAlso check [the inbox] | twice."
	if !ok || got != want {
		t.Errorf("got %q, %v; want %q, true", got, ok, want)
	}
}

func TestRetimeScheduledText_NameWithSeparatorSurvives(t *testing.T) {
	in := FormatScheduledText("a | b", "", time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC), time.UTC)
	asOf := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

	got, ok := RetimeScheduledText(in, asOf)
	want := "[Scheduled trigger: a | b | 2026-07-08T12:00:00Z UTC | 2026-W28]"
	if !ok || got != want {
		t.Errorf("got %q, %v; want %q, true", got, ok, want)
	}
}

func TestRetimeScheduledText_UnknownZoneUnchanged(t *testing.T) {
	in := "[Scheduled: digest | 2026-07-07T12:00:00Z Mars/Olympus | 2026-W28]"

	got, ok := RetimeScheduledText(in, time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	if ok || got != in {
		t.Errorf("got %q, %v; want input, false", got, ok)
	}
}

func TestRetimeScheduledText_MangledTimestampUnchanged(t *testing.T) {
	in := "[Scheduled: digest | 2026-07-07 noon UTC | 2026-W28]"

	got, ok := RetimeScheduledText(in, time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	if ok || got != in {
		t.Errorf("got %q, %v; want input, false", got, ok)
	}
}

func TestRetimeScheduledText_MangledLastRunUnchanged(t *testing.T) {
	in := "[Scheduled: digest | 2026-07-07T12:00:00Z UTC | 2026-W28 | last run yesterday (6h ago)]"

	got, ok := RetimeScheduledText(in, time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	if ok || got != in {
		t.Errorf("got %q, %v; want input, false", got, ok)
	}
}

func TestRetimeScheduledText_PlainTextUnchanged(t *testing.T) {
	in := "What is on my calendar today? [Scheduled: x | 2026-07-07T12:00:00Z UTC | 2026-W28]"

	got, ok := RetimeScheduledText(in, time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC))
	if ok || got != in {
		t.Errorf("got %q, %v; want input, false", got, ok)
	}
}
