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
	if loc == nil {
		loc = time.UTC
	}
	t := fireTime.In(loc)
	label := "Scheduled trigger: " + name
	if skill != "" {
		label = "Scheduled: " + skill
	}
	isoYear, isoWeek := t.ISOWeek()
	return fmt.Sprintf("[%s | %s %s | %04d-W%02d]", label, t.Format(time.RFC3339), loc, isoYear, isoWeek)
}
