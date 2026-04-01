package scheduler

// Schedule expression parsing.
//
// Supported formats:
//
//	Named shortcuts:
//	  @hourly              - every hour at minute 0
//	  @daily, @midnight    - every day at 00:00 UTC
//	  @weekly              - every Sunday at 00:00 UTC
//	  @monthly             - 1st of every month at 00:00 UTC
//	  @yearly, @annually   - 1st January every year at 00:00 UTC
//
//	Interval syntax:
//	  @every <duration>    - fixed interval (e.g. @every 5m, @every 1h30m)
//	  Duration uses Go's time.ParseDuration units: ns, us, ms, s, m, h
//
//	5-field cron syntax:
//	  <min> <hour> <dom> <month> <dow>
//	  Fields support: * (all), n (exact), n-m (range), */n or n-m/n (step),
//	  and comma-separated combinations of the above.
//	  Example: "0 8 * * 1-5" fires at 08:00 UTC on weekdays.
//	  Day-of-week: 0 and 7 both mean Sunday.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type exprKind int

const (
	kindInterval exprKind = iota
	kindCron
)

// parsedExpr is the internal representation of a parsed schedule expression.
type parsedExpr struct {
	kind     exprKind
	interval time.Duration // kindInterval only
	cron     *cronSpec     // kindCron only
}

// namedSchedules maps @shorthand names to 5-field cron expressions.
var namedSchedules = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
}

// ValidateExpr checks whether a schedule expression is syntactically valid.
// It is used by callers to validate configuration before registering schedules.
func ValidateExpr(s string) error {
	_, err := parseScheduleExpr(s)
	return err
}

func parseScheduleExpr(s string) (*parsedExpr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("schedule expression must not be empty")
	}
	if strings.HasPrefix(s, "@") {
		return parseAtExpr(s)
	}
	return parseCronExpr(s)
}

func parseAtExpr(s string) (*parsedExpr, error) {
	lower := strings.ToLower(s)

	if cronStr, ok := namedSchedules[lower]; ok {
		spec, err := parseCronSpec(cronStr)
		if err != nil {
			// Built-in expressions are always valid; this is a programming error.
			panic("built-in cron expression is invalid: " + err.Error())
		}
		return &parsedExpr{kind: kindCron, cron: spec}, nil
	}

	const everyPrefix = "@every "
	if strings.HasPrefix(lower, everyPrefix) {
		durStr := strings.TrimSpace(s[len(everyPrefix):])
		d, err := time.ParseDuration(durStr)
		if err != nil {
			return nil, fmt.Errorf("invalid @every duration %q — use Go duration format (e.g. 30m, 1h30m, 24h)", durStr)
		}
		if d <= 0 {
			return nil, fmt.Errorf("@every duration must be positive, got %q", durStr)
		}
		return &parsedExpr{kind: kindInterval, interval: d}, nil
	}

	return nil, fmt.Errorf(
		"unknown schedule shorthand %q; valid shorthands: @hourly, @daily, @midnight, @weekly, @monthly, @yearly, @annually, @every <duration>",
		s,
	)
}

func parseCronExpr(s string) (*parsedExpr, error) {
	spec, err := parseCronSpec(s)
	if err != nil {
		return nil, err
	}
	return &parsedExpr{kind: kindCron, cron: spec}, nil
}

// ---------------------------------------------------------------------------
// Cron spec
// ---------------------------------------------------------------------------

// cronSpec holds a parsed 5-field cron expression as bitsets for O(1) matching.
type cronSpec struct {
	minute bitset // valid range: 0–59
	hour   bitset // valid range: 0–23
	dom    bitset // valid range: 1–31
	month  bitset // valid range: 1–12
	dow    bitset // valid range: 0–6  (0 = Sunday)
}

// bitset is a bitmask for integer membership. Supports values 0–63.
type bitset uint64

func (b bitset) has(n int) bool {
	if n < 0 || n > 63 {
		return false
	}
	return b&(1<<uint(n)) != 0
}

// matches reports whether t matches this cron spec (evaluated in UTC).
func (c *cronSpec) matches(t time.Time) bool {
	t = t.UTC()
	return c.minute.has(t.Minute()) &&
		c.hour.has(t.Hour()) &&
		c.dom.has(t.Day()) &&
		c.month.has(int(t.Month())) &&
		c.dow.has(int(t.Weekday()))
}

// next returns the next time strictly after `after` that matches the spec.
// Returns zero time if no match is found within one year.
func (c *cronSpec) next(after time.Time) time.Time {
	t := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(366 * 24 * time.Hour)
	for t.Before(limit) {
		if c.matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// parseCronSpec parses a 5-field cron expression: minute hour dom month dow.
func parseCronSpec(expr string) (*cronSpec, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have exactly 5 fields, got %d in %q", len(fields), expr)
	}

	minute, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute field %q: %w", fields[0], err)
	}
	hour, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour field %q: %w", fields[1], err)
	}
	dom, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month field %q: %w", fields[2], err)
	}
	month, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month field %q: %w", fields[3], err)
	}
	// Allow 7 as an alias for Sunday (0) per POSIX cron convention.
	dow, err := parseCronField(fields[4], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("day-of-week field %q: %w", fields[4], err)
	}
	if dow.has(7) {
		dow |= 1 << 0  // set Sunday bit
		dow &^= 1 << 7 // clear the alias bit
	}

	return &cronSpec{minute: minute, hour: hour, dom: dom, month: month, dow: dow}, nil
}

// parseCronField parses a single cron field, which may be comma-separated.
func parseCronField(s string, min, max int) (bitset, error) {
	if !strings.Contains(s, ",") {
		return parseCronPart(s, min, max)
	}
	var result bitset
	for _, part := range strings.Split(s, ",") {
		b, err := parseCronPart(strings.TrimSpace(part), min, max)
		if err != nil {
			return 0, err
		}
		result |= b
	}
	return result, nil
}

// parseCronPart parses a single cron field part: *, n, n-m, */n, or n-m/n.
func parseCronPart(s string, min, max int) (bitset, error) {
	step := 1
	base := s

	// Extract optional /step suffix.
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		var err error
		step, err = strconv.Atoi(s[idx+1:])
		if err != nil || step <= 0 {
			return 0, fmt.Errorf("invalid step value %q", s[idx+1:])
		}
		base = s[:idx]
	}

	var start, end int
	switch {
	case base == "*":
		start, end = min, max
	case strings.Contains(base, "-"):
		idx := strings.Index(base, "-")
		var err1, err2 error
		start, err1 = strconv.Atoi(base[:idx])
		end, err2 = strconv.Atoi(base[idx+1:])
		if err1 != nil || err2 != nil {
			return 0, fmt.Errorf("invalid range %q", base)
		}
	default:
		n, err := strconv.Atoi(base)
		if err != nil {
			return 0, fmt.Errorf("invalid value %q (expected integer)", base)
		}
		start, end = n, n
	}

	if start < min || end > max || start > end {
		return 0, fmt.Errorf("value %d–%d out of allowed range [%d, %d]", start, end, min, max)
	}

	var result bitset
	for i := start; i <= end; i += step {
		result |= 1 << uint(i) // #nosec G115 -- i is bounded by cron field range (max 59)
	}
	return result, nil
}
