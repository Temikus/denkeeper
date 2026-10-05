package configmcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/audit"
)

const (
	approvalAuditDefaultLimit = 20
	approvalAuditMaxLimit     = 100
	// Byte caps on free text in each event, so a long error or reason cannot
	// flood the model's context.
	approvalAuditSummaryBytes = 300
	approvalAuditReasonBytes  = 500
)

// approvalAuditCategories is the whole surface of approval_audit. Other
// categories carry tool results and prompts, which this view must not expose.
var approvalAuditCategories = []string{audit.CategorySupervisor, audit.CategoryApproval}

// approvalAuditDetailKeys are the detail fields worth reading back. The rest
// (arguments, raw_response, answers) is large and already in the transcript.
// would_decide is left out on purpose: a shadow verdict never affects the call,
// and showing it would teach the agent what the decider flags.
var approvalAuditDetailKeys = []string{"tool", "decision", "cause", "reason", "stage", "mode", "supervisor", "decider", "scope", "error"}

type approvalAuditEvent struct {
	ID             int64          `json:"id"`
	Timestamp      time.Time      `json:"timestamp"`
	Category       string         `json:"category"`
	Action         string         `json:"action"`
	Status         string         `json:"status"`
	Source         string         `json:"source"`
	Summary        string         `json:"summary"`
	ConversationID string         `json:"conversation_id,omitempty"`
	Detail         map[string]any `json:"detail,omitempty"`
}

type approvalAuditResult struct {
	Events []approvalAuditEvent `json:"events"`
	Total  int                  `json:"total"` // matching events before limit
}

func (s *Server) registerAuditTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name: "approval_audit",
		Description: "Read your own supervisor and approval audit events, newest first. Use it to find out why a tool call did not run: " +
			"a supervisor review event with status error carries detail.cause (cost_limit, timeout, provider_error or too_large), " +
			"and a decider review has source decider:<name>. Only your agent's events in the supervisor and approval categories are returned. " +
			"'total' counts matches before 'limit'.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"category": {"type": "string", "enum": ["supervisor", "approval"], "description": "Restrict to one category (omit for both)"},
				"status":   {"type": "string", "description": "Comma-separated statuses to keep, e.g. \"error,denied\" (omit for all)"},
				"search":   {"type": "string", "description": "Substring of the event summary. Supervisor summaries name the tool, e.g. run_javascript"},
				"days":     {"type": "integer", "minimum": 0, "description": "Only events from the last N days (0 or absent = everything retained)"},
				"limit":    {"type": "integer", "minimum": 1, "description": "Max events returned (default 20, max 100)"}
			}
		}`),
	}, s.handleApprovalAudit)
}

func (s *Server) handleApprovalAudit(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		Category string `json:"category"`
		Status   string `json:"status"`
		Search   string `json:"search"`
		Days     int    `json:"days"`
		Limit    int    `json:"limit"`
	}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
			return toolError("invalid arguments: " + err.Error()), nil
		}
	}

	opts, errText := s.approvalAuditOpts(input.Category, input.Status, input.Search, input.Days, input.Limit)
	if errText != "" {
		return toolError(errText), nil
	}
	events, total, err := s.deps.AuditStore.List(ctx, opts)
	if err != nil {
		return toolError("listing audit events: " + err.Error()), nil
	}

	out := approvalAuditResult{Events: make([]approvalAuditEvent, 0, len(events)), Total: total}
	for i := range events {
		out.Events = append(out.Events, compactAuditEvent(&events[i]))
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolError("marshaling audit events: " + err.Error()), nil
	}
	return toolText(string(b)), nil
}

// approvalAuditOpts builds the store query. Agent and categories are fixed
// here, never taken from input: they are the scope of the view.
func (s *Server) approvalAuditOpts(category, status, search string, days, limit int) (audit.ListOpts, string) {
	opts := audit.ListOpts{
		Agent:      s.deps.AgentName,
		Categories: approvalAuditCategories,
		Statuses:   audit.ParseFilterList(status),
		Search:     search,
		Limit:      approvalAuditDefaultLimit,
	}
	switch category {
	case "":
	case audit.CategorySupervisor, audit.CategoryApproval:
		opts.Categories = []string{category}
	default:
		return opts, "invalid category: must be supervisor or approval"
	}
	switch {
	case days < 0:
		return opts, "invalid days: must not be negative"
	case days > 0:
		since := time.Now().UTC().AddDate(0, 0, -days)
		opts.Since = &since
	}
	switch {
	case limit < 0:
		return opts, "invalid limit: must be positive"
	case limit > approvalAuditMaxLimit:
		opts.Limit = approvalAuditMaxLimit
	case limit > 0:
		opts.Limit = limit
	}
	return opts, ""
}

// compactAuditEvent keeps the fields that explain a decision and drops the
// bulky rest. A detail that is not a JSON object is left out.
func compactAuditEvent(ev *audit.Event) approvalAuditEvent {
	out := approvalAuditEvent{
		ID:             ev.ID,
		Timestamp:      ev.Timestamp,
		Category:       ev.Category,
		Action:         ev.Action,
		Status:         ev.Status,
		Source:         ev.Source,
		Summary:        truncateUTF8(ev.Summary, approvalAuditSummaryBytes),
		ConversationID: ev.ConversationID,
	}
	var detail map[string]any
	if json.Unmarshal([]byte(ev.Detail), &detail) != nil {
		return out
	}
	for _, key := range approvalAuditDetailKeys {
		v, ok := detail[key]
		if !ok {
			continue
		}
		if str, isStr := v.(string); isStr {
			v = truncateUTF8(str, approvalAuditReasonBytes)
		}
		if out.Detail == nil {
			out.Detail = make(map[string]any)
		}
		out.Detail[key] = v
	}
	return out
}
