package audit

import (
	"context"
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
	if _, ok, _ := s.ReviewMark(ctx, "argus", "jev"); ok {
		t.Error("mark leaked to another agent")
	}
	if _, ok, _ := s.ReviewMark(ctx, "pamela", "other"); ok {
		t.Error("mark leaked to another decider")
	}
}

func TestReviewMark_PruneLeavesMarks(t *testing.T) {
	s := marksStore(t)
	setMark(t, s, "pamela", "jev", markBase, "a")
	if _, err := s.PruneBefore(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ReviewMark(context.Background(), "pamela", "jev"); !ok {
		t.Fatal("prune removed the mark")
	}
}
