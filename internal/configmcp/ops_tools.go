package configmcp

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/opsreport"
)

const opsReportDescription = "Return your own computed health numbers for the last 'days' days (default 7, max 30), " +
	"so you do not add up get_cost_summary, kv_list and schedule_list yourself. " +
	"spend: cost_usd and turns for the window and for the window before it, with change_usd and change_pct " +
	"(null when the earlier window had no spend). It counts completed turns only, not supervisor or decider reviews. " +
	"tools and skills: call counts split into failure_count (the broken-tool signal), rejection_count, denial_count, " +
	"approval_timeout_count and supervisor_error_count. Do not sum them into an error rate. " +
	"skills also carry runs, cost_per_run_usd and completion_tokens_per_run, counted only for turns one skill owned. " +
	"schedules: expected_fires from the current expression against actual_fires, the scheduled turns you started; " +
	"expected_fires assumes the current schedule ran all window, so a new or edited schedule shows a shortfall " +
	"(first_fire shows when it started); one that sends to several chats will not line up either. " +
	"kv: live keys and keys with no expiry, in total and per namespace (the text before the first ':'). " +
	"Only your own data is returned. Spend, tools and skills are counted per conversation: if a shared channel's " +
	"conversation was moved to you from another agent, its earlier turns count as yours. " +
	"'unavailable' names any part that could not be computed."

func (s *Server) registerOpsTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "ops_report",
		Description: opsReportDescription,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"days": {"type": "integer", "minimum": 0, "description": "Window length in days (default 7, max 30)"}
			}
		}`),
	}, s.handleOpsReport)
}

// handleOpsReport decodes only days: the agent is always Deps.AgentName and
// the clock is the server's, so no argument can widen or move the report.
func (s *Server) handleOpsReport(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Days int `json:"days"`
	}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
			return toolError("invalid arguments: " + err.Error()), nil
		}
	}
	rep, err := s.deps.OpsReport.Build(ctx, opsreport.Query{Agent: s.deps.AgentName, Days: input.Days})
	if err != nil {
		return toolError("ops report: " + err.Error()), nil
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return toolError("ops report: encoding: " + err.Error()), nil
	}
	return toolText(string(data)), nil
}
