package scheduler

import (
	"fmt"
	"time"
)

// FormatScheduledText builds the synthetic message text injected when a
// schedule fires, embedding the authoritative fire time and ISO week so the
// agent never has to infer "today" (design/heartbeat-improvements-2026-07.md
// Step 1.3). Dated KV keys and week buckets should derive from these values.
//
// Example: [Scheduled: heartbeat | 2026-07-07T10:45:00+10:00 Australia/Sydney | 2026-W28]
func FormatScheduledText(name, skill string, fireTime time.Time, loc *time.Location) string {
	return FormatScheduledTextWithPrev(name, skill, fireTime, time.Time{}, loc)
}

// FormatScheduledTextWithPrev is FormatScheduledText plus the previous fire
// of the same schedule, so a skill can compare against its last run without
// doing date arithmetic itself (which it gets wrong). A zero prevRun omits
// the segment rather than printing "never": a first fire after a restart must
// not read as a reason to do extra work.
//
// Example: [Scheduled: heartbeat | 2026-07-07T10:45:00+10:00 Australia/Sydney | 2026-W28 | last run 2026-07-07T07:45:00+10:00 (3h ago)]
func FormatScheduledTextWithPrev(name, skill string, fireTime, prevRun time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	t := fireTime.In(loc)
	label := "Scheduled trigger: " + name
	if skill != "" {
		label = "Scheduled: " + skill
	}
	isoYear, isoWeek := t.ISOWeek()
	head := fmt.Sprintf("[%s | %s %s | %04d-W%02d", label, t.Format(time.RFC3339), loc, isoYear, isoWeek)
	if prevRun.IsZero() {
		return head + "]"
	}
	return fmt.Sprintf("%s | last run %s (%s ago)]", head, prevRun.In(loc).Format(time.RFC3339), humanAgo(fireTime.Sub(prevRun)))
}

// humanAgo renders a duration at the coarsest unit that still reads as
// "recent": minutes under an hour, hours under two days, days beyond.
func humanAgo(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute)/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Round(time.Hour)/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d.Round(24*time.Hour)/(24*time.Hour)))
	}
}
