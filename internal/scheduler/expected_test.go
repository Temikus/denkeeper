package scheduler

import (
	"testing"
	"time"
)

var expBase = time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)

func mustExpected(t *testing.T, expr string, from, to time.Time, loc *time.Location) int {
	t.Helper()
	n, err := ExpectedFires(expr, from, to, loc)
	if err != nil {
		t.Fatalf("ExpectedFires(%q): %v", expr, err)
	}
	return n
}

func TestExpectedFires_DailyCronOverSevenDays(t *testing.T) {
	from := expBase.Add(time.Hour) // 01:00, so the 08:00 fires are well inside
	if n := mustExpected(t, "0 8 * * *", from, from.Add(7*24*time.Hour), nil); n != 7 {
		t.Fatalf("got %d, want 7", n)
	}
}

func TestExpectedFires_HalfOpenWindow(t *testing.T) {
	from := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	if n := mustExpected(t, "0 8 * * *", from, from.Add(24*time.Hour), nil); n != 1 {
		t.Fatalf("got %d, want 1 (fire at from counts, fire at to does not)", n)
	}
	// A from with seconds starts at the next whole minute.
	if n := mustExpected(t, "0 8 * * *", from.Add(time.Second), from.Add(24*time.Hour), nil); n != 0 {
		t.Fatalf("got %d, want 0", n)
	}
}

func TestExpectedFires_EveryInterval(t *testing.T) {
	if n := mustExpected(t, "@every 6h", expBase, expBase.Add(7*24*time.Hour), nil); n != 28 {
		t.Fatalf("got %d, want 28", n)
	}
}

// Sydney is UTC+11 in November, so 08:00 Sydney is 21:00 UTC the day before.
// The UTC window 2026-11-16 00:00 to 2026-11-16 22:00 holds one Sydney
// 08:00 (21:00 UTC) and, in UTC, one 08:00 too; shift it to end at 20:00
// and the Sydney count drops to 0 while UTC keeps 1.
func TestExpectedFires_UsesLocation(t *testing.T) {
	syd, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	from := expBase
	to := expBase.Add(20 * time.Hour)
	if n := mustExpected(t, "0 8 * * *", from, to, time.UTC); n != 1 {
		t.Fatalf("UTC: got %d, want 1", n)
	}
	if n := mustExpected(t, "0 8 * * *", from, to, syd); n != 0 {
		t.Fatalf("Sydney: got %d, want 0", n)
	}
}

func TestExpectedFires_InvalidExpression(t *testing.T) {
	if _, err := ExpectedFires("not a cron", expBase, expBase.Add(time.Hour), nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestExpectedFires_EmptyWindow(t *testing.T) {
	if n := mustExpected(t, "* * * * *", expBase, expBase, nil); n != 0 {
		t.Fatalf("to == from: got %d", n)
	}
	if n := mustExpected(t, "@every 1m", expBase, expBase.Add(-time.Hour), nil); n != 0 {
		t.Fatalf("to < from: got %d", n)
	}
}
