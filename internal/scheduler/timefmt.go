package scheduler

import (
	"fmt"
	"regexp"
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

// scheduledHeader matches a header FormatScheduledTextWithPrev wrote, at the
// start of the text. The name is lazy but must be followed by a timestamp,
// zone and ISO week, so a name containing " | " still parses.
var scheduledHeader = regexp.MustCompile(
	`^\[(Scheduled: |Scheduled trigger: )(.+?) \| (\S+) (\S+) \| \d{4}-W\d{2}(?: \| last run (\S+) \(\d+[mhd] ago\))?\]`)

// RetimeScheduledText re-renders a scheduled header as if it fired at
// fireTime, in the zone the header names. A last-run segment keeps its gap to
// the fire time. Text after the header is kept. Anything it cannot parse is
// returned unchanged with false.
func RetimeScheduledText(text string, fireTime time.Time) (string, bool) {
	m := scheduledHeader.FindStringSubmatchIndex(text)
	if m == nil {
		return text, false
	}
	group := func(i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return text[m[2*i]:m[2*i+1]]
	}
	oldFire, err := time.Parse(time.RFC3339, group(3))
	if err != nil {
		return text, false
	}
	loc, err := time.LoadLocation(group(4))
	if err != nil {
		return text, false
	}
	var prev time.Time
	if last := group(5); last != "" {
		oldPrev, err := time.Parse(time.RFC3339, last)
		if err != nil {
			return text, false
		}
		prev = fireTime.Add(-oldFire.Sub(oldPrev))
	}
	name, skill := group(2), ""
	if group(1) == "Scheduled: " {
		name, skill = "", name
	}
	return FormatScheduledTextWithPrev(name, skill, fireTime, prev, loc) + text[m[1]:], true
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
