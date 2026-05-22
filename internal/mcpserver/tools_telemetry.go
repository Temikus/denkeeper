package mcpserver

import (
	"context"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type costSummaryInput struct{}

type telemetrySummaryInput struct {
	Since string `json:"since,omitempty" jsonschema:"Start time (RFC 3339) for filtering"`
	Until string `json:"until,omitempty" jsonschema:"End time (RFC 3339) for filtering"`
}

func (s *Server) registerTelemetryTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "cost_summary",
		Description: "Get current cost tracking data: global cost, per-session costs, " +
			"per-agent costs, and configured limits. Requires 'costs:read' scope.",
	}, s.handleCostSummary)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "telemetry_summary",
		Description: "Get aggregate telemetry: total messages, tokens, costs, and tool calls. " +
			"Optional time range via 'since' and 'until' (RFC 3339). Requires 'costs:read' scope.",
	}, s.handleTelemetrySummary)
}

func (s *Server) handleCostSummary(ctx context.Context, _ *mcp.CallToolRequest, _ costSummaryInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "costs:read"); err != nil {
		return err, nil, nil
	}

	if s.deps.CostTracker == nil {
		return toolError("cost tracking not available"), nil, nil
	}

	r, err := toolJSON(map[string]any{
		"global_cost":   s.deps.CostTracker.GlobalCost(),
		"agent_costs":   s.deps.CostTracker.AgentCosts(),
		"session_costs": s.deps.CostTracker.AllSessionStats(),
	})
	return r, nil, err
}

func (s *Server) handleTelemetrySummary(ctx context.Context, _ *mcp.CallToolRequest, input telemetrySummaryInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "costs:read"); err != nil {
		return err, nil, nil
	}

	ts, ok := s.deps.Memory.(agent.TelemetryStore)
	if !ok {
		return toolError("telemetry not available"), nil, nil
	}

	var since, until *time.Time
	if input.Since != "" {
		t, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return toolError("invalid since: " + err.Error()), nil, nil
		}
		since = &t
	}
	if input.Until != "" {
		t, err := time.Parse(time.RFC3339, input.Until)
		if err != nil {
			return toolError("invalid until: " + err.Error()), nil, nil
		}
		until = &t
	}

	summary, err := ts.GetTelemetrySummary(ctx, since, until)
	if err != nil {
		return toolError("telemetry query failed: " + err.Error()), nil, nil
	}

	r, jsonErr := toolJSON(summary)
	return r, nil, jsonErr
}
