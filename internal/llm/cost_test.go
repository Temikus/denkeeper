package llm

import (
	"sync"
	"testing"
)

func TestCostTracker_Record(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 1.0}, nil)

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
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	ct.Record("s1", 1.0)
	ct.Record("s2", 2.0)

	if ct.GlobalCost() != 3.0 {
		t.Errorf("global cost = %f, want 3.0", ct.GlobalCost())
	}
}

func TestCostTracker_ConcurrentRecords(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 1000.0}, nil)
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

func TestCostTracker_ZeroHardLimit_DisablesLimit(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 0}, nil)

	// Zero hard limit means disabled — any amount should be within budget.
	if !ct.Record("s1", 100.0) {
		t.Error("expected within budget when hard limit is 0 (disabled)")
	}
	if ct.ExceedsBudget("s1") {
		t.Error("expected ExceedsBudget to be false when hard limit is 0 (disabled)")
	}
}

func TestCostTracker_RecordWithTokens(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)

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

func TestCostTracker_RecordWithTokens_PricingSource(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)

	ct.RecordWithTokens("agent1:telegram:1", 0.5, 100, 50, "registry")
	ct.RecordWithTokens("agent1:telegram:1", 0.3, 80, 40, "registry")
	ct.RecordWithTokens("agent1:telegram:1", 0.1, 10, 5, "provider")

	stats := ct.AllSessionStats()
	s := stats["agent1:telegram:1"]
	if s.PricingSources["registry"] != 2 {
		t.Errorf("registry count = %d, want 2", s.PricingSources["registry"])
	}
	if s.PricingSources["provider"] != 1 {
		t.Errorf("provider count = %d, want 1", s.PricingSources["provider"])
	}
}

func TestCostTracker_RecordWithTokens_NoPricingSource(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)
	ct.RecordWithTokens("sess", 0.5, 100, 50)

	stats := ct.AllSessionStats()
	s := stats["sess"]
	if s.PricingSources != nil {
		t.Errorf("expected nil PricingSources when not provided, got %v", s.PricingSources)
	}
}

func TestCostTracker_AgentCosts(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)

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

func TestCostTracker_RegisterSessionAgent(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)

	// Register agent name for a channel-based session ID.
	ct.RegisterSessionAgent("chan:work", "assistant")
	ct.RecordWithTokens("chan:work", 1.0, 100, 50)

	// Also record with a standard session ID.
	ct.RecordWithTokens("helper:tg:1", 0.5, 50, 25)

	agents := ct.AgentCosts()
	byName := make(map[string]AgentStats)
	for _, a := range agents {
		byName[a.Agent] = a
	}

	// Channel session should be attributed to "assistant", not "chan".
	if _, ok := byName["chan"]; ok {
		t.Error("should not have agent named 'chan'")
	}
	assistant, ok := byName["assistant"]
	if !ok {
		t.Fatal("assistant not found in agent costs")
	}
	if assistant.Cost != 1.0 {
		t.Errorf("assistant cost = %f, want 1.0", assistant.Cost)
	}

	// Standard session should still work via ID parsing.
	helper, ok := byName["helper"]
	if !ok {
		t.Fatal("helper not found in agent costs")
	}
	if helper.Cost != 0.5 {
		t.Errorf("helper cost = %f, want 0.5", helper.Cost)
	}
}

func TestCostTracker_RegisterSessionAgent_LimitsEnforcement(t *testing.T) {
	overrides := map[string]SessionLimits{
		"premium": {Hard: 100.0},
	}
	ct := NewCostTracker(SessionLimits{Hard: 1.0}, overrides)

	// Channel session mapped to "premium" agent should use premium limits.
	ct.RegisterSessionAgent("chan:vip", "premium")
	ct.Record("chan:vip", 50.0)

	if ct.ExceedsHardLimit("chan:vip") {
		t.Error("chan:vip should use premium limits (100.0), not default (1.0)")
	}
}

func TestCostTracker_RecordAlsoPopulatesStats(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)

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
	ct := NewCostTracker(SessionLimits{Hard: 10.0}, nil)

	if cost := ct.SessionCost("never-seen"); cost != 0 {
		t.Errorf("unknown session cost = %f, want 0", cost)
	}
	if ct.ExceedsBudget("never-seen") {
		t.Error("unknown session should not exceed budget")
	}
}

// --- Soft and hard limit tests ---

func TestCostTracker_SoftLimit(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Soft: 0.5, Hard: 1.0}, nil)

	ct.Record("s1", 0.4)
	if ct.ExceedsSoftLimit("s1") {
		t.Error("expected NOT to exceed soft limit at 0.4")
	}

	ct.Record("s1", 0.2) // total 0.6 > soft 0.5
	if !ct.ExceedsSoftLimit("s1") {
		t.Error("expected to exceed soft limit at 0.6")
	}
	if ct.ExceedsHardLimit("s1") {
		t.Error("expected NOT to exceed hard limit at 0.6")
	}
}

func TestCostTracker_HardLimit(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Soft: 0.5, Hard: 1.0}, nil)

	ct.Record("s1", 1.1) // over hard limit
	if !ct.ExceedsHardLimit("s1") {
		t.Error("expected to exceed hard limit at 1.1")
	}
	if !ct.ExceedsSoftLimit("s1") {
		t.Error("expected to also exceed soft limit at 1.1")
	}
}

func TestCostTracker_SoftDisabled(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Soft: 0, Hard: 1.0}, nil)

	ct.Record("s1", 100.0)
	if ct.ExceedsSoftLimit("s1") {
		t.Error("soft limit should be disabled when set to 0")
	}
}

func TestCostTracker_AgentOverrides(t *testing.T) {
	ct := NewCostTracker(
		SessionLimits{Soft: 0.5, Hard: 1.0},
		map[string]SessionLimits{
			"expensive": {Soft: 5.0, Hard: 10.0},
		},
	)

	// Default agent session
	ct.Record("default:tg:1", 0.6)
	if !ct.ExceedsSoftLimit("default:tg:1") {
		t.Error("default agent should exceed soft limit at 0.6 (limit=0.5)")
	}

	// Overridden agent session
	ct.Record("expensive:tg:1", 0.6)
	if ct.ExceedsSoftLimit("expensive:tg:1") {
		t.Error("expensive agent should NOT exceed soft limit at 0.6 (limit=5.0)")
	}

	ct.Record("expensive:tg:1", 5.0) // total 5.6 > 5.0
	if !ct.ExceedsSoftLimit("expensive:tg:1") {
		t.Error("expensive agent should exceed soft limit at 5.6 (limit=5.0)")
	}
}

func TestCostTracker_DefaultLimits(t *testing.T) {
	limits := SessionLimits{Soft: 0.8, Hard: 2.0}
	ct := NewCostTracker(limits, nil)

	got := ct.DefaultLimits()
	if got.Soft != 0.8 || got.Hard != 2.0 {
		t.Errorf("DefaultLimits = %+v, want %+v", got, limits)
	}
}

func TestCostTracker_SetAgentLimits(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 1.0}, nil)

	ct.SetAgentLimits("special", SessionLimits{Hard: 100.0})
	ct.Record("special:tg:1", 50.0)
	if ct.ExceedsHardLimit("special:tg:1") {
		t.Error("expected NOT to exceed hard limit for overridden agent")
	}
}

func TestCostTracker_MaxBudgetPerSession_Deprecated(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Hard: 3.14}, nil)
	if ct.MaxBudgetPerSession() != 3.14 {
		t.Errorf("MaxBudgetPerSession = %f, want 3.14", ct.MaxBudgetPerSession())
	}
}

func TestCostTracker_SetDefaultLimits(t *testing.T) {
	ct := NewCostTracker(SessionLimits{Soft: 1.0, Hard: 5.0}, nil)

	if got := ct.DefaultLimits(); got.Soft != 1.0 || got.Hard != 5.0 {
		t.Fatalf("initial limits = %+v, want {1.0, 5.0}", got)
	}

	ct.SetDefaultLimits(SessionLimits{Soft: 2.0, Hard: 10.0})
	if got := ct.DefaultLimits(); got.Soft != 2.0 || got.Hard != 10.0 {
		t.Errorf("after update = %+v, want {2.0, 10.0}", got)
	}

	// New sessions should use the updated limits.
	ct.Record("new-session:tg:1", 3.0)
	if !ct.ExceedsSoftLimit("new-session:tg:1") {
		t.Error("expected to exceed new soft limit of 2.0 at cost 3.0")
	}
	if ct.ExceedsHardLimit("new-session:tg:1") {
		t.Error("expected NOT to exceed new hard limit of 10.0 at cost 3.0")
	}
}
