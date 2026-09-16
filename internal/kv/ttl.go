package kv

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// dwTerm matches one numeric day- or week-suffixed term (e.g. "30d", "2.5w")
// so it can be rewritten into hours before time.ParseDuration sees it. No
// standard Go duration unit contains "d" or "w", so this rewrite can never
// corrupt any other term.
var dwTerm = regexp.MustCompile(`[0-9]+(\.[0-9]+)?[dw]`)

// ParseTTL parses a TTL string. It accepts everything time.ParseDuration
// does, plus "d" (24h) and "w" (168h) as ordinary duration terms, so
// "30d", "2w", and mixed forms like "1d12h" work alongside plain Go
// durations such as "90m".
func ParseTTL(s string) (time.Duration, error) {
	rewritten := dwTerm.ReplaceAllStringFunc(s, rewriteDayWeekTerm)
	d, err := time.ParseDuration(rewritten)
	if err != nil {
		return 0, fmt.Errorf(
			"invalid ttl %q: expected a duration such as \"30d\", \"2w\", \"24h\", \"45m\" (units: w, d, h, m, s, ms, us, ns): %w",
			s, err,
		)
	}
	return d, nil
}

// rewriteDayWeekTerm converts one "<number>d" or "<number>w" term to its
// equivalent in hours. Returns the term unchanged if it cannot parse as a
// number, leaving the original error to surface from time.ParseDuration.
func rewriteDayWeekTerm(term string) string {
	unit := term[len(term)-1]
	n, err := strconv.ParseFloat(term[:len(term)-1], 64)
	if err != nil {
		return term
	}

	var hours float64
	switch unit {
	case 'd':
		hours = n * 24
	case 'w':
		hours = n * 168
	}
	return strconv.FormatFloat(hours, 'f', -1, 64) + "h"
}
