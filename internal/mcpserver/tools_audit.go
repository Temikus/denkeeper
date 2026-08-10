package mcpserver

import (
	"context"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type auditEventsInput struct {
	Category      string `json:"category,omitempty" jsonschema:"Filter by category (tool_call, skill, channel, approval, schedule, llm, config, session, mcp, safety, supervisor); comma-separated for several"`
	Agent         string `json:"agent,omitempty" jsonschema:"Filter by agent name"`
	Status        string `json:"status,omitempty" jsonschema:"Filter by status (ok, error, pending, denied); comma-separated for several"`
	Source        string `json:"source,omitempty" jsonschema:"Filter by event source"`
	ExcludeSource string `json:"exclude_source,omitempty" jsonschema:"Drop events with these sources (e.g. eval,dryrun); comma-separated for several"`
	Search        string `json:"search,omitempty" jsonschema:"Free-text search across event summaries"`
	Since         string `json:"since,omitempty" jsonschema:"Start of time range (RFC 3339)"`
	Until         string `json:"until,omitempty" jsonschema:"End of time range (RFC 3339)"`
	Limit         int    `json:"limit,omitempty" jsonschema:"Max results (default 50, max 200)"`
	Offset        int    `json:"offset,omitempty" jsonschema:"Pagination offset"`
}

type auditSummaryInput struct {
	Since         string `json:"since,omitempty" jsonschema:"Only count events after this time (RFC 3339)"`
	ExcludeSource string `json:"exclude_source,omitempty" jsonschema:"Drop events with these sources (e.g. eval,dryrun); comma-separated for several"`
}

func (s *Server) registerAuditTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "audit_events",
		Description: "List audit log events with optional filtering by category, agent, status, " +
			"source, free-text search, and time range. 'category' and 'status' accept a " +
			"comma-separated list and match any of the given values; 'exclude_source' drops " +
			"the listed sources (dry-run and eval turns emit under the ordinary llm/tool_call " +
			"categories, so excluding by source is the only way to hide them). " +
			"Supports pagination (default limit 50, max 200). Requires 'audit:read' scope.",
	}, s.handleAuditEvents)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "audit_summary",
		Description: "Get aggregate audit statistics: total events, counts by category and status, " +
			"and events in the last hour. Optional 'since' (RFC 3339) time filter and " +
			"'exclude_source' to keep dry-run/eval events out of the counts. " +
			"Requires 'audit:read' scope.",
	}, s.handleAuditSummary)
}

func (s *Server) handleAuditEvents(ctx context.Context, _ *mcp.CallToolRequest, input auditEventsInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "audit:read"); err != nil {
		return err, nil, nil
	}
	if s.deps.AuditStore == nil {
		return toolError("audit not configured"), nil, nil
	}

	opts := audit.ListOpts{
		Categories:     audit.ParseFilterList(input.Category),
		Agent:          input.Agent,
		Statuses:       audit.ParseFilterList(input.Status),
		Source:         input.Source,
		ExcludeSources: audit.ParseFilterList(input.ExcludeSource),
		Search:         input.Search,
		Limit:          input.Limit,
		Offset:         input.Offset,
	}
	if input.Since != "" {
		t, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return toolError("invalid since: " + err.Error()), nil, nil
		}
		opts.Since = &t
	}
	if input.Until != "" {
		t, err := time.Parse(time.RFC3339, input.Until)
		if err != nil {
			return toolError("invalid until: " + err.Error()), nil, nil
		}
		opts.Until = &t
	}

	events, total, err := s.deps.AuditStore.List(ctx, opts)
	if err != nil {
		return toolError("listing audit events: " + err.Error()), nil, nil
	}
	if events == nil {
		events = []audit.Event{}
	}

	r, jsonErr := toolJSON(audit.ListResult{
		Events: events,
		Total:  total,
		Limit:  opts.Limit,
		Offset: opts.Offset,
	})
	return r, nil, jsonErr
}

func (s *Server) handleAuditSummary(ctx context.Context, _ *mcp.CallToolRequest, input auditSummaryInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "audit:read"); err != nil {
		return err, nil, nil
	}
	if s.deps.AuditStore == nil {
		return toolError("audit not configured"), nil, nil
	}

	opts := audit.StatsOpts{ExcludeSources: audit.ParseFilterList(input.ExcludeSource)}
	if input.Since != "" {
		t, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return toolError("invalid since: " + err.Error()), nil, nil
		}
		opts.Since = &t
	}

	stats, err := s.deps.AuditStore.Stats(ctx, opts)
	if err != nil {
		return toolError("getting audit stats: " + err.Error()), nil, nil
	}

	r, jsonErr := toolJSON(stats)
	return r, nil, jsonErr
}
