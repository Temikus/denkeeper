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

func TestCostTracker_RecordWithTokens(t *testing.T) {
	ct := NewCostTracker(10.0)

	ct.RecordWithTokens("agent1:telegram:123", 0.5, 100, 50)
	ct.RecordWithTokens("agent1:telegram:123", 0.3, 80, 40)

	stats := ct.AllSessionStats()
	s, ok := stats["agent1:telegram:123"]
	if !ok {
		t.Fatal("session stats not found")
	}
	if s.InputTokens != 180 {
		t.Errorf("input tokens = %d, want 180", s.InputTokens)
	}
	if s.OutputTokens != 90 {
		t.Errorf("output tokens = %d, want 90", s.OutputTokens)
	}
	if s.Messages != 2 {
		t.Errorf("messages = %d, want 2", s.Messages)
	}
}

func TestCostTracker_AgentCosts(t *testing.T) {
	ct := NewCostTracker(10.0)

	ct.RecordWithTokens("alice:telegram:1", 1.0, 100, 50)
	ct.RecordWithTokens("alice:telegram:2", 0.5, 50, 25)
	ct.RecordWithTokens("bob:discord:1", 2.0, 200, 100)

	agents := ct.AgentCosts()
	byName := make(map[string]AgentStats)
	for _, a := range agents {
		byName[a.Agent] = a
	}

	alice, ok := byName["alice"]
	if !ok {
		t.Fatal("alice not found")
	}
	if alice.Cost != 1.5 {
		t.Errorf("alice cost = %f, want 1.5", alice.Cost)
	}
	if alice.Sessions != 2 {
		t.Errorf("alice sessions = %d, want 2", alice.Sessions)
	}
	if alice.InputTokens != 150 {
		t.Errorf("alice input_tokens = %d, want 150", alice.InputTokens)
	}

	bob, ok := byName["bob"]
	if !ok {
		t.Fatal("bob not found")
	}
	if bob.Sessions != 1 {
		t.Errorf("bob sessions = %d, want 1", bob.Sessions)
	}
	if bob.Cost != 2.0 {
		t.Errorf("bob cost = %f, want 2.0", bob.Cost)
	}
}

func TestCostTracker_AgentFromSessionID(t *testing.T) {
	if got := agentFromSessionID("myagent:telegram:123"); got != "myagent" {
		t.Errorf("got %q, want myagent", got)
	}
	if got := agentFromSessionID("plain-id"); got != "plain-id" {
		t.Errorf("got %q, want plain-id", got)
	}
}

func TestCostTracker_RecordAlsoPopulatesStats(t *testing.T) {
	ct := NewCostTracker(10.0)

	// The old Record() method should also populate sessionStats for backward compat.
	ct.Record("agent:test:1", 0.5)
	stats := ct.AllSessionStats()
	s, ok := stats["agent:test:1"]
	if !ok {
		t.Fatal("Record should populate session stats")
	}
	if s.Messages != 1 {
		t.Errorf("messages = %d, want 1", s.Messages)
	}
	if s.Cost != 0.5 {
		t.Errorf("cost = %f, want 0.5", s.Cost)
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
