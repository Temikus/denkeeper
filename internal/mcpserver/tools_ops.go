package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/opsreport"
)

// opsReportInput has no clock field: the window always ends now.
type opsReportInput struct {
	Agent string `json:"agent" jsonschema:"Agent to report on"`
	Days  int    `json:"days,omitempty" jsonschema:"Window length in days (default 7, max 30)"`
}

func (s *Server) registerOpsTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "ops_report",
		Description: "Computed health report for one agent over the last 'days' days (default 7, max 30): " +
			"spend against the window before, per-tool and per-skill outcome counts, per-skill cost and " +
			"completion tokens per run, expected against actual schedule fires, and KV key counts by " +
			"namespace. Use it instead of aggregating audit_events. Spend counts completed turns only. " +
			"failure_count is the broken-tool signal; denials and timeouts are separate counts. " +
			"expected_fires assumes the current schedule ran all window, so a new or edited schedule " +
			"shows a shortfall (first_fire shows when it started); skill runs count only turns one skill " +
			"owned; kv counts live keys now. Telemetry is scoped by conversation: if a shared channel " +
			"was moved from one agent to another, the new agent's report includes the earlier agent's " +
			"turns in that conversation. 'unavailable' names any part that could not be computed. " +
			"Requires 'audit:read' scope.",
	}, s.handleOpsReport)
}

func (s *Server) handleOpsReport(ctx context.Context, _ *mcp.CallToolRequest, input opsReportInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "audit:read"); err != nil {
		return err, nil, nil
	}
	if input.Agent == "" {
		return toolError("agent is required"), nil, nil
	}
	if s.deps.Dispatcher == nil || s.deps.Dispatcher.Agent(input.Agent) == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}
	rep := opsreport.New(s.deps.Memory, s.deps.KVStore, s.deps.Scheduler, s.deps.AuditStore)
	if rep == nil {
		return toolError("ops report not available"), nil, nil
	}
	report, err := rep.Build(ctx, opsreport.Query{Agent: input.Agent, Days: input.Days})
	if err != nil {
		return toolError("ops report: " + err.Error()), nil, nil
	}
	r, jsonErr := toolJSON(report)
	return r, nil, jsonErr
}
