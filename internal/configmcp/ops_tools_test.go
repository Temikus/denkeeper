package configmcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/opsreport"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

// opsStores are the real stores an ops report reads.
type opsStores struct {
	mem   *agent.SQLiteMemoryStore
	raw   *sqlx.DB // second handle on the memory file, to backdate rows
	kv    *kv.SQLiteStore
	aud   *audit.SQLiteStore
	sched *scheduler.Scheduler
}

func newOpsStores(t *testing.T) *opsStores {
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
	return &opsStores{mem: mem, raw: raw, kv: kvs, aud: aud,
		sched: scheduler.New(slog.New(slog.NewTextHandler(io.Discard, nil)), time.UTC)}
}

// seed gives agentName a turn with a tool call and an owning skill, a KV key,
// a schedule and one scheduled trigger, each named after the agent.
func (s *opsStores) seed(t *testing.T, agentName string, cost float64) {
	t.Helper()
	ctx := context.Background()
	convID, err := s.mem.GetOrCreateConversation(ctx, "test", agentName+"-conv")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := s.mem.AddMessage(ctx, convID, agent.StoredMessage{Role: "user", Content: "q"})
	if err != nil {
		t.Fatal(err)
	}
	reply := agent.StoredMessage{Role: "assistant", Content: "a", Model: "m", Cost: cost, TokensCompletion: 10}
	replyID, err := s.mem.AddMessage(ctx, convID, reply)
	if err != nil {
		t.Fatal(err)
	}
	calls := []agent.ToolCallRecord{{ToolName: agentName + "_tool", ServerName: "srv", Round: 1, Success: true,
		Outcome: "ok", SkillName: agentName + "_skill", SkillVersion: "1"}}
	if err := s.mem.AddToolCalls(ctx, convID, replyID, calls); err != nil {
		t.Fatal(err)
	}
	if err := s.mem.AddSkillUsages(ctx, convID, userID,
		[]agent.SkillUsageRecord{{SkillName: agentName + "_skill", MatchType: "schedule"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.mem.UpdateConversationStats(ctx, convID, agentName, reply, len(calls), 0); err != nil {
		t.Fatal(err)
	}
	// Rows are stamped at whole seconds and the window ends before "now",
	// so a row from this second would fall outside it. Move them back.
	ts := time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05")
	for _, table := range []string{"messages", "tool_calls", "message_skills"} {
		if _, err := s.raw.Exec("UPDATE "+table+" SET created_at = ? WHERE conversation_id = ?", ts, convID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.kv.Set(ctx, agentName, agentName+"_ns:k", "v", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.sched.Register(scheduler.Config{Name: agentName + "_sched", Type: "agent", Schedule: "@every 1h",
		Skill: agentName + "_skill", Agent: agentName, Enabled: true}, func(scheduler.Entry) {}); err != nil {
		t.Fatal(err)
	}
	s.trigger(t, agentName, agentName+"_sched")
}

func (s *opsStores) trigger(t *testing.T, agentName, schedule string) {
	t.Helper()
	if err := s.aud.Insert(context.Background(), audit.Event{
		Timestamp: time.Now().UTC().Add(-time.Hour), Category: audit.CategorySession, Action: "trigger",
		Agent: agentName, Summary: "trigger", Status: audit.StatusOK, Source: "scheduler",
		Detail: fmt.Sprintf(`{"trigger_type":"schedule","schedule_name":%q}`, schedule),
	}); err != nil {
		t.Fatal(err)
	}
}

func newOpsServer(t *testing.T, s *opsStores) *mcp.ClientSession {
	t.Helper()
	rep := opsreport.New(s.mem, s.kv, s.sched, s.aud)
	if rep == nil {
		t.Fatal("opsreport.New returned nil for real stores")
	}
	session, _ := newTestServer(t, func(d *configmcp.Deps) { d.OpsReport = rep })
	return session
}

type opsReportResponse struct {
	Agent  string `json:"agent"`
	Window struct {
		Days int `json:"days"`
	} `json:"window"`
	Spend struct {
		CostUSD float64 `json:"cost_usd"`
		Turns   int     `json:"turns"`
	} `json:"spend"`
}

func callOpsReport(t *testing.T, session *mcp.ClientSession, args map[string]any) (string, opsReportResponse) {
	t.Helper()
	text, isErr := callTool(t, session, "ops_report", args)
	if isErr {
		t.Fatalf("ops_report error: %s", text)
	}
	var resp opsReportResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("decode ops_report: %v\n%s", err, text)
	}
	return text, resp
}

func TestOpsReport_NotRegisteredWithoutReporter(t *testing.T) {
	session, _ := newTestServer(t, nil)
	assertToolRegistered(t, session, "ops_report", false)
}

func TestOpsReport_NotRegisteredWithoutAgentName(t *testing.T) {
	s := newOpsStores(t)
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.OpsReport = opsreport.New(s.mem, s.kv, s.sched, s.aud)
		d.AgentName = ""
	})
	assertToolRegistered(t, session, "ops_report", false)
}

func TestOpsReport_RegisteredWithNoAgentArgument(t *testing.T) {
	session := newOpsServer(t, newOpsStores(t))
	assertToolRegistered(t, session, "ops_report", true)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range result.Tools {
		if tl.Name != "ops_report" {
			continue
		}
		schema, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(schema), `"agent"`) || !strings.Contains(string(schema), `"days"`) {
			t.Errorf("input schema = %s, want days and no agent", schema)
		}
		if !strings.Contains(tl.Description, "conversation was moved to you from another agent") {
			t.Errorf("description does not state the per-conversation scoping limit: %s", tl.Description)
		}
	}
}

// The issue's hard constraint, at the tool: every source holds rows for both
// agents, and test-agent's report holds only its own.
func TestOpsReport_NeverAnotherAgentsData(t *testing.T) {
	s := newOpsStores(t)
	s.seed(t, "test-agent", 0.25)
	s.seed(t, "other-agent", 0.75)
	// A dry-run trigger must not count as one of test-agent's fires.
	s.trigger(t, "test-agent#dryrun", "test-agent_sched")
	session := newOpsServer(t, s)

	text, resp := callOpsReport(t, session, map[string]any{})
	if strings.Contains(text, "other-agent") || strings.Contains(text, "other") {
		t.Fatalf("report mentions the other agent:\n%s", text)
	}
	if resp.Agent != "test-agent" || resp.Spend.CostUSD != 0.25 || resp.Spend.Turns != 1 {
		t.Errorf("report = %+v, want test-agent's spend only", resp)
	}
	for _, want := range []string{"test-agent_tool", "test-agent_skill", "test-agent_sched", "test-agent_ns",
		`"actual_fires": 1`} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
}

func TestOpsReport_AgentArgumentIsIgnored(t *testing.T) {
	s := newOpsStores(t)
	s.seed(t, "test-agent", 0.25)
	s.seed(t, "other-agent", 0.75)
	session := newOpsServer(t, s)

	text, resp := callOpsReport(t, session, map[string]any{"agent": "other-agent"})
	if resp.Agent != "test-agent" || resp.Spend.CostUSD != 0.25 || strings.Contains(text, "other-agent") {
		t.Errorf("agent argument changed the report:\n%s", text)
	}
}

func TestOpsReport_Days(t *testing.T) {
	session := newOpsServer(t, newOpsStores(t))
	text, isErr := callTool(t, session, "ops_report", map[string]any{"days": -1})
	if !isErr || !strings.Contains(text, "ops report: ") {
		t.Errorf("days -1 = %q (error %v), want a tool error", text, isErr)
	}
	if _, resp := callOpsReport(t, session, map[string]any{"days": 45}); resp.Window.Days != 30 {
		t.Errorf("days 45 -> window.days %d, want 30", resp.Window.Days)
	}
}
