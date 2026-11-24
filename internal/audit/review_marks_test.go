package audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func marksStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func setMark(t *testing.T, s *SQLiteStore, agent, decider string, at time.Time, by string) ReviewMark {
	t.Helper()
	m, err := s.SetReviewMark(context.Background(), ReviewMark{Agent: agent, Decider: decider, ReviewedUpTo: at, ReviewedBy: by})
	if err != nil {
		t.Fatalf("SetReviewMark: %v", err)
	}
	return m
}

var markBase = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func TestReviewMark_NotFound(t *testing.T) {
	_, ok, err := marksStore(t).ReviewMark(context.Background(), "pamela", "jev")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v, want not found", ok, err)
	}
}

func TestReviewMark_StoresAndReturns(t *testing.T) {
	s := marksStore(t)
	got := setMark(t, s, "pamela", "jev", markBase, "ops-key")
	if !got.ReviewedUpTo.Equal(markBase) || got.ReviewedBy != "ops-key" || got.UpdatedAt.IsZero() {
		t.Fatalf("returned %+v", got)
	}
	read, ok, err := s.ReviewMark(context.Background(), "pamela", "jev")
	if err != nil || !ok || !read.ReviewedUpTo.Equal(markBase) || read.ReviewedBy != "ops-key" {
		t.Fatalf("read %+v ok=%v err=%v", read, ok, err)
	}
}

func TestReviewMark_NeverMovesBack(t *testing.T) {
	s := marksStore(t)
	setMark(t, s, "pamela", "jev", markBase, "first")
	got := setMark(t, s, "pamela", "jev", markBase.Add(-time.Hour), "second")
	if !got.ReviewedUpTo.Equal(markBase) || got.ReviewedBy != "first" {
		t.Fatalf("older mark moved it: %+v", got)
	}
}

// "10:00:00Z" sorts after "10:00:00.5Z" as text, but is earlier in time.
func TestReviewMark_SubSecondOrder(t *testing.T) {
	s := marksStore(t)
	later := markBase.Add(500 * time.Millisecond)
	setMark(t, s, "pamela", "jev", later, "a")
	if got := setMark(t, s, "pamela", "jev", markBase, "b"); !got.ReviewedUpTo.Equal(later) {
		t.Fatalf("earlier same-second mark won: %+v", got)
	}
	s2 := marksStore(t)
	setMark(t, s2, "pamela", "jev", markBase, "a")
	if got := setMark(t, s2, "pamela", "jev", later, "b"); !got.ReviewedUpTo.Equal(later) || got.ReviewedBy != "b" {
		t.Fatalf("later same-second mark did not advance: %+v", got)
	}
}

func TestReviewMark_SeparatePerAgentAndDecider(t *testing.T) {
	s := marksStore(t)
	setMark(t, s, "pamela", "jev", markBase, "a")
	ctx := context.Background()
	for _, key := range [][2]string{{"argus", "jev"}, {"pamela", "other"}} {
		_, ok, err := s.ReviewMark(ctx, key[0], key[1])
		if err != nil {
			t.Fatalf("ReviewMark(%s, %s): %v", key[0], key[1], err)
		}
		if ok {
			t.Errorf("mark leaked to %s/%s", key[0], key[1])
		}
	}
}

// Prune must actually delete something here, or the test proves nothing about
// whether marks survive a sweep.
func TestReviewMark_PruneLeavesMarks(t *testing.T) {
	s := marksStore(t)
	ctx := context.Background()
	setMark(t, s, "pamela", "jev", markBase, "a")
	if err := s.Insert(ctx, Event{Timestamp: markBase.Add(-48 * time.Hour), Category: CategorySupervisor,
		Action: "review", Agent: "pamela", Status: StatusOK}); err != nil {
		t.Fatal(err)
	}
	pruned, err := s.PruneBefore(ctx, markBase.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned == 0 {
		t.Fatal("prune deleted nothing; the test must reach the delete path")
	}
	_, ok, err := s.ReviewMark(ctx, "pamela", "jev")
	if err != nil {
		t.Fatalf("ReviewMark: %v", err)
	}
	if !ok {
		t.Fatal("prune removed the mark")
	}
}

// UnixNano overflows before 1677-09-21 and wraps to the far future, which
// would store a mark nothing can ever move past.
func TestReviewMark_OutOfRangeTimeIsRefused(t *testing.T) {
	s := marksStore(t)
	ctx := context.Background()
	for _, at := range []time.Time{time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := s.SetReviewMark(ctx, ReviewMark{Agent: "pamela", Decider: "jev", ReviewedUpTo: at}); !errors.Is(err, ErrReviewMarkOutOfRange) {
			t.Errorf("SetReviewMark(%v) err = %v, want ErrReviewMarkOutOfRange", at, err)
		}
	}
	_, ok, err := s.ReviewMark(ctx, "pamela", "jev")
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v, want nothing stored", ok, err)
	}
	if !ReviewMarkInRange(markBase) || ReviewMarkInRange(time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("ReviewMarkInRange disagrees with SetReviewMark")
	}
}
