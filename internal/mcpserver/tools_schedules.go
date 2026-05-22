package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type scheduleListInput struct{}

func (s *Server) registerScheduleTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "schedule_list",
		Description: "List all schedules with name, cron expression, skill, agent, status, " +
			"and last/next run times. Requires 'schedules:read' scope.",
	}, s.handleScheduleList)
}

func (s *Server) handleScheduleList(ctx context.Context, _ *mcp.CallToolRequest, _ scheduleListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "schedules:read"); err != nil {
		return err, nil, nil
	}

	if s.deps.Scheduler == nil {
		return toolError("scheduler not available"), nil, nil
	}

	type schedInfo struct {
		Name    string `json:"name"`
		Expr    string `json:"expression"`
		Skill   string `json:"skill,omitempty"`
		Agent   string `json:"agent,omitempty"`
		Channel string `json:"channel,omitempty"`
		Enabled bool   `json:"enabled"`
		LastRun string `json:"last_run,omitempty"`
		NextRun string `json:"next_run,omitempty"`
	}

	entries := s.deps.Scheduler.AgentEntries()
	result := make([]schedInfo, len(entries))
	for i, e := range entries {
		si := schedInfo{
			Name:    e.Name,
			Expr:    e.Expr,
			Skill:   e.Skill,
			Agent:   e.Agent,
			Channel: e.Channel,
			Enabled: e.Enabled,
		}
		if !e.LastRun.IsZero() {
			si.LastRun = e.LastRun.Format("2006-01-02T15:04:05Z07:00")
		}
		if !e.NextRun.IsZero() {
			si.NextRun = e.NextRun.Format("2006-01-02T15:04:05Z07:00")
		}
		result[i] = si
	}

	r, err := toolJSON(result)
	return r, nil, err
}
