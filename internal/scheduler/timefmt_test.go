package scheduler

import (
	"log/slog"
	"testing"
	"time"
)

func mustLoadSydney(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Fatalf("loading Australia/Sydney: %v", err)
	}
	return loc
}

func TestFormatScheduledText_SkillLabel(t *testing.T) {
	sydney := mustLoadSydney(t)
	fire := time.Date(2026, 7, 7, 10, 45, 0, 0, sydney)

	got := FormatScheduledText("heartbeat", "heartbeat", fire, sydney)
	want := "[Scheduled: heartbeat | 2026-07-07T10:45:00+10:00 Australia/Sydney | 2026-W28]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduledText_TriggerLabelWhenNoSkill(t *testing.T) {
	fire := time.Date(2026, 7, 7, 0, 45, 0, 0, time.UTC)

	got := FormatScheduledText("nightly-ping", "", fire, time.UTC)
	want := "[Scheduled trigger: nightly-ping | 2026-07-07T00:45:00Z UTC | 2026-W28]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduledText_NilLocationDefaultsUTC(t *testing.T) {
	sydney := mustLoadSydney(t)
	fire := time.Date(2026, 7, 7, 10, 45, 0, 0, sydney) // 00:45 UTC

	got := FormatScheduledText("heartbeat", "heartbeat", fire, nil)
	want := "[Scheduled: heartbeat | 2026-07-07T00:45:00Z UTC | 2026-W28]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduledText_SundayStaysInISOWeek(t *testing.T) {
	// Sunday belongs to the *ending* ISO week — the skill-forge Sunday-run
	// bug keyed itself to the following week.
	sydney := mustLoadSydney(t)
	fire := time.Date(2026, 7, 12, 11, 30, 0, 0, sydney) // Sunday

	got := FormatScheduledText("skill-forge-weekly", "skill-forge", fire, sydney)
	want := "[Scheduled: skill-forge | 2026-07-12T11:30:00+10:00 Australia/Sydney | 2026-W28]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduledText_MondayRollsISOWeek(t *testing.T) {
	sydney := mustLoadSydney(t)
	fire := time.Date(2026, 7, 13, 10, 15, 0, 0, sydney) // Monday

	got := FormatScheduledText("self-audit-weekly", "self-audit", fire, sydney)
	want := "[Scheduled: self-audit | 2026-07-13T10:15:00+10:00 Australia/Sydney | 2026-W29]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduledText_ISOYearBoundary(t *testing.T) {
	// 2026-01-01 is a Thursday, so 2026 has 53 ISO weeks and Friday
	// 2027-01-01 still belongs to 2026-W53.
	fire := time.Date(2027, 1, 1, 8, 0, 0, 0, time.UTC)

	got := FormatScheduledText("daily", "daily", fire, time.UTC)
	want := "[Scheduled: daily | 2027-01-01T08:00:00Z UTC | 2026-W53]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatScheduledText_TimezoneChangesISOWeek(t *testing.T) {
	// The same instant is Sunday (W28) in UTC but already Monday (W29) in
	// Sydney — the reason headers render in the agent's timezone.
	sydney := mustLoadSydney(t)
	fire := time.Date(2026, 7, 12, 14, 30, 0, 0, time.UTC)

	gotUTC := FormatScheduledText("heartbeat", "heartbeat", fire, time.UTC)
	wantUTC := "[Scheduled: heartbeat | 2026-07-12T14:30:00Z UTC | 2026-W28]"
	if gotUTC != wantUTC {
		t.Errorf("UTC: got %q, want %q", gotUTC, wantUTC)
	}

	gotSyd := FormatScheduledText("heartbeat", "heartbeat", fire, sydney)
	wantSyd := "[Scheduled: heartbeat | 2026-07-13T00:30:00+10:00 Australia/Sydney | 2026-W29]"
	if gotSyd != wantSyd {
		t.Errorf("Sydney: got %q, want %q", gotSyd, wantSyd)
	}
}

func TestFormatScheduledText_DSTOffset(t *testing.T) {
	// Sydney DST starts Sunday 2026-10-04 at 02:00 (+10:00 → +11:00).
	sydney := mustLoadSydney(t)

	before := time.Date(2026, 10, 3, 12, 0, 0, 0, sydney)
	gotBefore := FormatScheduledText("heartbeat", "heartbeat", before, sydney)
	wantBefore := "[Scheduled: heartbeat | 2026-10-03T12:00:00+10:00 Australia/Sydney | 2026-W40]"
	if gotBefore != wantBefore {
		t.Errorf("before DST: got %q, want %q", gotBefore, wantBefore)
	}

	after := time.Date(2026, 10, 5, 12, 0, 0, 0, sydney)
	gotAfter := FormatScheduledText("heartbeat", "heartbeat", after, sydney)
	wantAfter := "[Scheduled: heartbeat | 2026-10-05T12:00:00+11:00 Australia/Sydney | 2026-W41]"
	if gotAfter != wantAfter {
		t.Errorf("after DST: got %q, want %q", gotAfter, wantAfter)
	}
}

func TestScheduler_LocationGetter(t *testing.T) {
	logger := slog.Default()

	if got := New(logger, nil).Location(); got != time.UTC {
		t.Errorf("nil loc: got %v, want UTC", got)
	}

	sydney := mustLoadSydney(t)
	if got := New(logger, sydney).Location(); got != sydney {
		t.Errorf("explicit loc: got %v, want %v", got, sydney)
	}
}
