package opsreport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

// stores holds one real store per source. raw is a second handle on the
// memory file, used to backdate created_at.
type stores struct {
	mem   *agent.SQLiteMemoryStore
	raw   *sqlx.DB
	kv    *kv.SQLiteStore
	aud   *audit.SQLiteStore
	sched *scheduler.Scheduler
	now   time.Time
}

func newStores(t *testing.T) *stores {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory.db")
	mem, err := agent.NewSQLiteMemoryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	raw, err := sqlx.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	kvs, err := kv.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kvs.Close() })
	aud, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = aud.Close() })
	// Rows are stamped by CURRENT_TIMESTAMP at whole seconds; an hour of
	// headroom keeps them inside [now-7d, now).
	return &stores{
		mem: mem, raw: raw, kv: kvs, aud: aud,
		sched: scheduler.New(slog.New(slog.NewTextHandler(io.Discard, nil)), time.UTC),
		now:   time.Now().UTC().Add(time.Hour),
	}
}

func (s *stores) reporter() *Reporter { return New(s.mem, s.kv, s.sched, s.aud) }

func (s *stores) build(t *testing.T, agentName string, days int) *Report {
	t.Helper()
	rep, err := s.reporter().Build(context.Background(), Query{Agent: agentName, Days: days, Now: s.now})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return rep
}

type turn struct {
	agent, conv string
	cost        float64
	completion  int
	calls       []agent.ToolCallRecord
	skills      []agent.SkillUsageRecord
	backdate    time.Duration // > 0: move the turn this far before now
}

func (s *stores) addTurn(t *testing.T, tr turn) {
	t.Helper()
	ctx := context.Background()
	convID, err := s.mem.GetOrCreateConversation(ctx, "test", tr.conv)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := s.mem.AddMessage(ctx, convID, agent.StoredMessage{Role: "user", Content: "q"})
	if err != nil {
		t.Fatal(err)
	}
	reply := agent.StoredMessage{Role: "assistant", Content: "a", Model: "m", Cost: tr.cost, TokensCompletion: tr.completion}
	replyID, err := s.mem.AddMessage(ctx, convID, reply)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.calls) > 0 {
		if err := s.mem.AddToolCalls(ctx, convID, replyID, tr.calls); err != nil {
			t.Fatal(err)
		}
	}
	if len(tr.skills) > 0 {
		if err := s.mem.AddSkillUsages(ctx, convID, userID, tr.skills); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.mem.UpdateConversationStats(ctx, convID, tr.agent, reply, len(tr.calls), 0); err != nil {
		t.Fatal(err)
	}
	if tr.backdate > 0 {
		ts := s.now.Add(-tr.backdate).Format("2006-01-02 15:04:05")
		for _, table := range []string{"messages", "tool_calls", "message_skills"} {
			if _, err := s.raw.Exec("UPDATE "+table+" SET created_at = ? WHERE conversation_id = ?", ts, convID); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func (s *stores) addSchedule(t *testing.T, name, skill, agentName, expr string, enabled bool) {
	t.Helper()
	if err := s.sched.Register(scheduler.Config{
		Name: name, Type: "agent", Schedule: expr, Skill: skill, Agent: agentName, Enabled: enabled,
	}, func(scheduler.Entry) {}); err != nil {
		t.Fatal(err)
	}
}

func (s *stores) addTrigger(t *testing.T, agentName, schedule string, at time.Time) {
	t.Helper()
	if err := s.aud.Insert(context.Background(), audit.Event{
		Timestamp: at, Category: audit.CategorySession, Action: "trigger", Agent: agentName,
		Summary: "trigger", Status: audit.StatusOK, Source: "scheduler",
		Detail: fmt.Sprintf(`{"trigger_type":"schedule","schedule_name":%q}`, schedule),
	}); err != nil {
		t.Fatal(err)
	}
}

func (s *stores) addKey(t *testing.T, agentName, key string, ttl time.Duration) {
	t.Helper()
	if err := s.kv.Set(context.Background(), agentName, key, "v", ttl); err != nil {
		t.Fatal(err)
	}
}

func toolCall(tool, outcome, skill string) agent.ToolCallRecord {
	return agent.ToolCallRecord{ToolName: tool, ServerName: "srv", Round: 1, DurationMs: 10,
		Success: outcome == "ok", Outcome: outcome, SkillName: skill, SkillVersion: "1"}
}

func ptr[T any](v T) *T { return &v }

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- tests ---

func TestBuild_Fixture(t *testing.T) {
	s := newStores(t)
	s.addTurn(t, turn{agent: "pamela", conv: "c1", cost: 0.3, completion: 100,
		calls:  []agent.ToolCallRecord{toolCall("web_fetch", "ok", "brief"), toolCall("web_fetch", "failed", "brief")},
		skills: []agent.SkillUsageRecord{{SkillName: "brief", MatchType: "command"}}})
	s.addTurn(t, turn{agent: "pamela", conv: "c2", cost: 0.1, completion: 50,
		calls: []agent.ToolCallRecord{toolCall("kv_get", "denied", "")},
		skills: []agent.SkillUsageRecord{
			{SkillName: "heartbeat", MatchType: "schedule"}, {SkillName: "tone", MatchType: "always"}}})
	s.addTurn(t, turn{agent: "pamela", conv: "c3", cost: 0.2, backdate: 8 * 24 * time.Hour})
	s.addSchedule(t, "daily", "heartbeat", "pamela", "@every 24h", true)
	s.addSchedule(t, "paused", "heartbeat", "pamela", "0 8 * * *", false)
	first, last := s.now.Add(-2*time.Hour), s.now.Add(-time.Hour)
	s.addTrigger(t, "pamela", "daily", first)
	s.addTrigger(t, "pamela", "daily", last)
	s.addKey(t, "pamela", "log:a", 0)
	s.addKey(t, "pamela", "log:b", time.Hour)
	s.addKey(t, "pamela", "note", 0)

	got := s.build(t, "pamela", 7)
	span := 7 * 24 * time.Hour
	want := &Report{
		Agent:  "pamela",
		Window: Window{Days: 7, From: s.now.Add(-span), To: s.now, PreviousFrom: s.now.Add(-2 * span)},
		Spend: Spend{CostUSD: 0.4, Turns: 2, PreviousCostUSD: 0.2, PreviousTurns: 1,
			ChangeUSD: 0.2, ChangePct: ptr(100.0)},
		Tools: []agent.ToolUsageSummary{
			{ToolName: "web_fetch", ServerName: "srv", CallCount: 2, FailureCount: 1, AvgDuration: 10},
			{ToolName: "kv_get", ServerName: "srv", CallCount: 1, DenialCount: 1, AvgDuration: 10},
		},
		Skills: []Skill{
			{SkillName: "brief", Runs: 1, CostUSD: 0.3, CostPerRunUSD: 0.3, CompletionTokens: 100,
				CompletionTokensPerRun: 100, ToolCallCount: 2, FailureCount: 1},
			{SkillName: "heartbeat", Runs: 1, CostUSD: 0.1, CostPerRunUSD: 0.1, CompletionTokens: 50,
				CompletionTokensPerRun: 50},
		},
		Schedules: []Schedule{
			{Name: "daily", Skill: "heartbeat", Expression: "@every 24h", Enabled: true, ExpectedFires: 7,
				ActualFires: ptr(2), FirstFire: &first, LastFire: &last},
			{Name: "paused", Skill: "heartbeat", Expression: "0 8 * * *", ExpectedFires: 0, ActualFires: ptr(0)},
		},
		KV: &KV{Keys: 3, NoExpiry: 2, Namespaces: []Namespace{
			{Namespace: "log", Keys: 2, NoExpiry: 1}, {Namespace: "", Keys: 1, NoExpiry: 1}}},
	}
	if g, w := mustJSON(t, got), mustJSON(t, want); g != w {
		t.Fatalf("report mismatch\n got: %s\nwant: %s", g, w)
	}
}

// seedAgent gives one agent a row in every source, with names unique to it.
func seedAgent(s *stores, t *testing.T, name string, cost float64) {
	s.addTurn(t, turn{agent: name, conv: name + "-conv", cost: cost,
		calls:  []agent.ToolCallRecord{toolCall(name+"_tool", "ok", name+"_skill")},
		skills: []agent.SkillUsageRecord{{SkillName: name + "_skill", MatchType: "command"}}})
	s.addSchedule(t, name+"_sched", name+"_skill", name, "@every 1h", true)
	s.addTrigger(t, name, name+"_sched", s.now.Add(-time.Hour))
	s.addKey(t, name, name+"_ns:k", 0)
}

func TestBuild_NeverAnotherAgentsData(t *testing.T) {
	s := newStores(t)
	seedAgent(s, t, "pamela", 0.25)
	seedAgent(s, t, "argus", 0.75)
	// A dry-run trigger for pamela must not count as a fire.
	s.addTrigger(t, "pamela#dryrun", "pamela_sched", s.now.Add(-time.Hour))

	for _, pair := range [][2]string{{"pamela", "argus"}, {"argus", "pamela"}} {
		self, other := pair[0], pair[1]
		rep := s.build(t, self, 7)
		js := mustJSON(t, rep)
		if strings.Contains(js, other) {
			t.Fatalf("%s's report mentions %s: %s", self, other, js)
		}
		if rep.Spend.Turns != 1 || len(rep.Tools) != 1 || len(rep.Skills) != 1 || len(rep.Schedules) != 1 || rep.KV.Keys != 1 {
			t.Fatalf("%s's report has foreign rows: %s", self, js)
		}
		if *rep.Schedules[0].ActualFires != 1 {
			t.Fatalf("%s actual_fires = %d, want 1", self, *rep.Schedules[0].ActualFires)
		}
	}
	if got := s.build(t, "pamela", 7).Spend.CostUSD; got != 0.25 {
		t.Fatalf("pamela spend = %v, want 0.25", got)
	}
}

func TestBuild_EmptyAgentIsError(t *testing.T) {
	s := newStores(t)
	if _, err := s.reporter().Build(context.Background(), Query{}); err == nil {
		t.Fatal("expected an error for an empty agent")
	}
}

func TestBuild_DaysDefaultCapAndNegative(t *testing.T) {
	s := newStores(t)
	if d := s.build(t, "pamela", 0).Window.Days; d != 7 {
		t.Errorf("days 0 -> %d, want 7", d)
	}
	if d := s.build(t, "pamela", 45).Window.Days; d != 30 {
		t.Errorf("days 45 -> %d, want 30", d)
	}
	if _, err := s.reporter().Build(context.Background(), Query{Agent: "pamela", Days: -1}); err == nil {
		t.Error("days -1 accepted")
	}
}

func TestBuild_NoChangePctWithoutPreviousSpend(t *testing.T) {
	s := newStores(t)
	s.addTurn(t, turn{agent: "pamela", conv: "c1", cost: 0.5})
	rep := s.build(t, "pamela", 7)
	if rep.Spend.ChangePct != nil || !strings.Contains(mustJSON(t, rep), `"change_pct":null`) {
		t.Fatalf("change_pct = %v, want null in the JSON: %s", rep.Spend.ChangePct, mustJSON(t, rep))
	}
	if rep.Spend.ChangeUSD != 0.5 {
		t.Fatalf("change_usd = %v", rep.Spend.ChangeUSD)
	}
}

func TestBuild_NoAuditStore(t *testing.T) {
	s := newStores(t)
	s.addSchedule(t, "daily", "x", "pamela", "@every 24h", true)
	rep, err := New(s.mem, s.kv, s.sched, nil).Build(context.Background(), Query{Agent: "pamela", Now: s.now})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Schedules[0].ExpectedFires != 7 || rep.Schedules[0].ActualFires != nil {
		t.Fatalf("schedule = %+v", rep.Schedules[0])
	}
	if !reflect.DeepEqual(rep.Unavailable, []string{"schedules.actual_fires"}) {
		t.Fatalf("unavailable = %v", rep.Unavailable)
	}
	if strings.Contains(mustJSON(t, rep), `"actual_fires":`) {
		t.Fatal("actual_fires serialised without an audit log")
	}
}

func TestBuild_NoKVNoScheduler(t *testing.T) {
	s := newStores(t)
	rep, err := New(s.mem, nil, nil, s.aud).Build(context.Background(), Query{Agent: "pamela", Now: s.now})
	if err != nil {
		t.Fatal(err)
	}
	js := mustJSON(t, rep)
	if rep.KV != nil || strings.Contains(js, `"kv":`) {
		t.Fatalf("kv present: %s", js)
	}
	if !strings.Contains(js, `"schedules":[]`) {
		t.Fatalf("schedules not []: %s", js)
	}
	if !reflect.DeepEqual(rep.Unavailable, []string{"schedules", "kv"}) {
		t.Fatalf("unavailable = %v", rep.Unavailable)
	}
}

func TestBuild_DisabledScheduleExpectsZero(t *testing.T) {
	s := newStores(t)
	s.addSchedule(t, "off", "x", "pamela", "* * * * *", false)
	if n := s.build(t, "pamela", 7).Schedules[0].ExpectedFires; n != 0 {
		t.Fatalf("expected_fires = %d", n)
	}
}

func TestBuild_EmptyStoresGiveEmptyArrays(t *testing.T) {
	s := newStores(t)
	js := mustJSON(t, s.build(t, "pamela", 7))
	for _, want := range []string{`"tools":[]`, `"skills":[]`, `"schedules":[]`, `"namespaces":[]`} {
		if !strings.Contains(js, want) {
			t.Errorf("missing %s in %s", want, js)
		}
	}
}

func TestBuild_CapsToolsAndNamespaces(t *testing.T) {
	s := newStores(t)
	var calls []agent.ToolCallRecord
	for i := range 101 {
		calls = append(calls, toolCall(fmt.Sprintf("tool%03d", i), "ok", ""))
	}
	s.addTurn(t, turn{agent: "pamela", conv: "c1", calls: calls})
	kvs, err := kv.NewInMemoryStore(kv.WithMaxKeysPerAgent(200))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kvs.Close() })
	s.kv = kvs
	for i := range 51 {
		s.addKey(t, "pamela", fmt.Sprintf("ns%02d:k", i), 0)
	}

	rep := s.build(t, "pamela", 7)
	if len(rep.Tools) != 100 || rep.ToolsOmitted != 1 {
		t.Fatalf("tools %d, omitted %d", len(rep.Tools), rep.ToolsOmitted)
	}
	if len(rep.KV.Namespaces) != 50 || rep.KV.NamespacesOmitted != 1 || rep.KV.Keys != 51 {
		t.Fatalf("kv = %d namespaces, %d omitted, %d keys", len(rep.KV.Namespaces), rep.KV.NamespacesOmitted, rep.KV.Keys)
	}
}

func TestNew_NilForMemoryWithoutOpsQueries(t *testing.T) {
	s := newStores(t)
	hidden := struct{ agent.MemoryStore }{s.mem}
	if New(hidden, s.kv, s.sched, s.aud) != nil {
		t.Fatal("New accepted a store without the ops queries")
	}
}
