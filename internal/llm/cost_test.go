package llm

import "testing"

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
