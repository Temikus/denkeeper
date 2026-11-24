package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ScanEvents applies List's filters, returns every match in id order (not
// timestamp order), and stops at the first callback error.
func TestScanEvents_FiltersInIDOrderAndStopsOnError(t *testing.T) {
	s := marksStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for i, ev := range []Event{
		{Timestamp: base.Add(2 * time.Second), Category: CategorySupervisor, Agent: "pamela", Summary: "first"},
		{Timestamp: base, Category: CategorySupervisor, Agent: "pamela", Summary: "second, earlier time"},
		{Timestamp: base, Category: CategorySupervisor, Agent: "argus", Summary: "other agent"},
		{Timestamp: base, Category: CategoryLLM, Agent: "pamela", Summary: "other category"},
		{Timestamp: base.Add(-time.Hour), Category: CategorySupervisor, Agent: "pamela", Summary: "before since"},
	} {
		ev.Action, ev.Status = "review", StatusOK
		if err := s.Insert(ctx, ev); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	since := base.Add(-time.Minute)
	opts := ListOpts{Categories: []string{CategorySupervisor}, Agent: "pamela", Since: &since, Limit: 1, Offset: 5}

	var got []string
	if err := s.ScanEvents(ctx, opts, func(e Event) error { got = append(got, e.Summary); return nil }); err != nil {
		t.Fatalf("ScanEvents: %v", err)
	}
	if len(got) != 2 || got[0] != "first" || got[1] != "second, earlier time" {
		t.Fatalf("scanned %q, want first then second (id order; Limit and Offset ignored)", got)
	}

	stop := errors.New("stop")
	calls := 0
	err := s.ScanEvents(ctx, opts, func(Event) error { calls++; return stop })
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want stop after 1", err, calls)
	}
}
