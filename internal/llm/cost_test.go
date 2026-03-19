package llm

import (
	"sync"
	"testing"
)

func TestCostTracker_Record(t *testing.T) {
	ct := NewCostTracker(1.0)

	if !ct.Record("s1", 0.5) {
		t.Error("expected within budget")
	}
	if ct.SessionCost("s1") != 0.5 {
		t.Errorf("session cost = %f, want 0.5", ct.SessionCost("s1"))
	}

	if !ct.Record("s1", 0.5) {
		t.Error("expected within budget at exactly max")
	}

	if ct.Record("s1", 0.1) {
		t.Error("expected over budget")
	}
	if !ct.ExceedsBudget("s1") {
		t.Error("expected ExceedsBudget to be true")
	}
}

func TestCostTracker_GlobalCost(t *testing.T) {
	ct := NewCostTracker(10.0)
	ct.Record("s1", 1.0)
	ct.Record("s2", 2.0)

	if ct.GlobalCost() != 3.0 {
		t.Errorf("global cost = %f, want 3.0", ct.GlobalCost())
	}
}

func TestCostTracker_ConcurrentRecords(t *testing.T) {
	ct := NewCostTracker(1000.0)
	var wg sync.WaitGroup

	// 100 goroutines each recording 10 times
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				ct.Record("shared-session", 0.01)
			}
		}(i)
	}
	wg.Wait()

	// 100 * 10 * 0.01 = 10.0
	got := ct.SessionCost("shared-session")
	want := 10.0
	// Allow small float imprecision
	if got < want-0.001 || got > want+0.001 {
		t.Errorf("session cost = %f, want ~%f", got, want)
	}
}

func TestCostTracker_ZeroBudget(t *testing.T) {
	ct := NewCostTracker(0)

	// First record should return false (over budget since 0+any > 0)
	if ct.Record("s1", 0.001) {
		t.Error("expected over budget with zero max")
	}
	if !ct.ExceedsBudget("s1") {
		t.Error("expected ExceedsBudget to be true")
	}
}

func TestCostTracker_NewSessionCostIsZero(t *testing.T) {
	ct := NewCostTracker(10.0)

	if cost := ct.SessionCost("never-seen"); cost != 0 {
		t.Errorf("unknown session cost = %f, want 0", cost)
	}
	if ct.ExceedsBudget("never-seen") {
		t.Error("unknown session should not exceed budget")
	}
}
