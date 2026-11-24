package scheduler

import (
	"fmt"
	"time"
)

// ExpectedFires counts how often expr would fire in [from, to), evaluated in
// loc (nil is UTC). It assumes the expression was live for the whole window.
// For @every it is the number of whole intervals, which is approximate: the
// ticker restarts with the process.
func ExpectedFires(expr string, from, to time.Time, loc *time.Location) (int, error) {
	p, err := parseScheduleExpr(expr)
	if err != nil {
		return 0, fmt.Errorf("expected fires: %w", err)
	}
	if !to.After(from) {
		return 0, nil
	}
	if p.kind == kindInterval {
		return int(to.Sub(from) / p.interval), nil
	}
	if loc == nil {
		loc = time.UTC
	}
	m := from.Truncate(time.Minute)
	if m.Before(from) {
		m = m.Add(time.Minute)
	}
	n := 0
	for ; m.Before(to); m = m.Add(time.Minute) {
		if p.cron.matches(m, loc) {
			n++
		}
	}
	return n, nil
}
