package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/opsreport"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

// opsServer wires real in-memory stores and a dispatcher with two agents.
func opsServer(t *testing.T, withAudit bool) (*Server, *agent.SQLiteMemoryStore) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mem, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	kvs, err := kv.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kvs.Close() })
	disp := agent.NewDispatcher(map[string]*agent.Engine{"test-agent": testEngine(t), "other-agent": testEngine(t)}, nil, nil, logger)
	deps := Deps{Dispatcher: disp, Memory: mem, KVStore: kvs, Scheduler: scheduler.New(logger, time.UTC), Logger: logger}
	if withAudit {
		aud, err := audit.NewInMemoryStore()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = aud.Close() })
		deps.AuditStore = aud
	}
	return &Server{deps: deps}, mem
}

// seedOpsAgent stores one turn with a tool call for agent, and one KV key.
func seedOpsAgent(t *testing.T, s *Server, mem *agent.SQLiteMemoryStore, name string, cost float64) {
	t.Helper()
	ctx := context.Background()
	convID, err := mem.GetOrCreateConversation(ctx, "test", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.AddMessage(ctx, convID, agent.StoredMessage{Role: "user", Content: "q"}); err != nil {
		t.Fatal(err)
	}
	reply := agent.StoredMessage{Role: "assistant", Content: "a", Model: "m", Cost: cost}
	replyID, err := mem.AddMessage(ctx, convID, reply)
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.AddToolCalls(ctx, convID, replyID, []agent.ToolCallRecord{
		{ToolName: name + "_tool", ServerName: "srv", Round: 1, Success: true, Outcome: "ok"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := mem.UpdateConversationStats(ctx, convID, name, reply, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.deps.KVStore.Set(ctx, name, name+"_ns:k", "v", 0); err != nil {
		t.Fatal(err)
	}
}

func callOps(t *testing.T, s *Server, ctx context.Context, in opsReportInput) (*mcp.CallToolResult, *opsreport.Report) {
	t.Helper()
	res, _, err := s.handleOpsReport(ctx, nil, in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		return res, nil
	}
	var rep opsreport.Report
	if err := json.Unmarshal([]byte(toolResultText(res)), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return res, &rep
}

func requireOpsError(t *testing.T, res *mcp.CallToolResult, want string) {
	t.Helper()
	if !res.IsError || !strings.Contains(toolResultText(res), want) {
		t.Fatalf("want error containing %q, got %q (IsError=%v)", want, toolResultText(res), res.IsError)
	}
}

func TestOpsReport_RequiresAuditRead(t *testing.T) {
	s, _ := opsServer(t, true)
	res, _ := callOps(t, s, withScopes(context.Background(), []string{"costs:read"}), opsReportInput{Agent: "test-agent"})
	requireOpsError(t, res, "insufficient scope")
}

func TestOpsReport_AgentRequiredAndKnown(t *testing.T) {
	s, _ := opsServer(t, true)
	res, _ := callOps(t, s, auditReadCtx(), opsReportInput{})
	requireOpsError(t, res, "agent is required")
	res, _ = callOps(t, s, auditReadCtx(), opsReportInput{Agent: "ghost"})
	requireOpsError(t, res, "agent not found: ghost")
}

func TestOpsReport_OnlyTheNamedAgentsNumbers(t *testing.T) {
	s, mem := opsServer(t, true)
	seedOpsAgent(t, s, mem, "test-agent", 0.25)
	seedOpsAgent(t, s, mem, "other-agent", 0.75)

	res, rep := callOps(t, s, auditReadCtx(), opsReportInput{Agent: "test-agent"})
	if rep == nil {
		t.Fatalf("tool error: %s", toolResultText(res))
	}
	if strings.Contains(toolResultText(res), "other-agent") {
		t.Fatalf("report mentions other-agent: %s", toolResultText(res))
	}
	if rep.Agent != "test-agent" || rep.Spend.CostUSD != 0.25 || rep.Spend.Turns != 1 ||
		len(rep.Tools) != 1 || rep.Tools[0].ToolName != "test-agent_tool" || rep.KV.Keys != 1 {
		t.Fatalf("report = %s", toolResultText(res))
	}
}

func TestOpsReport_Days(t *testing.T) {
	s, _ := opsServer(t, true)
	if _, rep := callOps(t, s, auditReadCtx(), opsReportInput{Agent: "test-agent"}); rep.Window.Days != 7 {
		t.Errorf("default days = %d, want 7", rep.Window.Days)
	}
	if _, rep := callOps(t, s, auditReadCtx(), opsReportInput{Agent: "test-agent", Days: 45}); rep.Window.Days != 30 {
		t.Errorf("days 45 -> %d, want 30", rep.Window.Days)
	}
	res, _ := callOps(t, s, auditReadCtx(), opsReportInput{Agent: "test-agent", Days: -1})
	requireOpsError(t, res, "invalid days")
}

func TestOpsReport_MemoryWithoutOpsQueries(t *testing.T) {
	s, mem := opsServer(t, true)
	s.deps.Memory = struct{ agent.MemoryStore }{mem}
	res, _ := callOps(t, s, auditReadCtx(), opsReportInput{Agent: "test-agent"})
	requireOpsError(t, res, "ops report not available")
}

func TestOpsReport_NoAuditStore(t *testing.T) {
	s, _ := opsServer(t, false)
	res, rep := callOps(t, s, auditReadCtx(), opsReportInput{Agent: "test-agent"})
	if rep == nil {
		t.Fatalf("tool error: %s", toolResultText(res))
	}
	if len(rep.Unavailable) != 1 || rep.Unavailable[0] != opsreport.UnavailableActualFires {
		t.Fatalf("unavailable = %v", rep.Unavailable)
	}
}

func TestOpsReport_Registered(t *testing.T) {
	s := &Server{deps: Deps{Logger: testLogger()}}
	s.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "denkeeper", Version: "test"}, nil)
	s.registerOpsTools()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.mcpServer.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "ops-test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range result.Tools {
		if tool.Name == "ops_report" {
			schema, _ := json.Marshal(tool.InputSchema)
			if strings.Contains(string(schema), `"now"`) {
				t.Fatalf("schema exposes now: %s", schema)
			}
			return
		}
	}
	t.Fatal("ops_report not in the tool list")
}
