package configmcp

import (
	"context"
	"encoding/json"
	"strings"
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
	// A search scans this many of the newest events, in pages of the store's
	// List cap.
	approvalAuditScanMax  = 2000
	approvalAuditScanPage = 200
)

// approvalAuditCategories is the whole surface of approval_audit. Other
// categories carry tool results and prompts, which this view must not expose.
var approvalAuditCategories = []string{audit.CategorySupervisor, audit.CategoryApproval}

// approvalAuditDetailKeys are the detail fields worth reading back. The rest
// (arguments, raw_response, answers) is large and already in the transcript.
// would_decide is left out on purpose: a shadow verdict never affects the call,
// and showing it would teach the agent what the decider flags. The verdict is
// also in a shadow event's summary and reason, so redactShadow strips those.
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
				"search":   {"type": "string", "description": "Case-insensitive substring of the event summary, checked against the newest 2000 events. Supervisor summaries name the tool, e.g. run_javascript"},
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

	opts, errText := s.approvalAuditOpts(input.Category, input.Status, input.Days, input.Limit)
	if errText != "" {
		return toolError(errText), nil
	}
	var out approvalAuditResult
	var err error
	if input.Search == "" {
		out, err = s.listApprovalAudit(ctx, opts)
	} else {
		out, err = s.searchApprovalAudit(ctx, opts, input.Search)
	}
	if err != nil {
		return toolError("listing audit events: " + err.Error()), nil
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolError("marshaling audit events: " + err.Error()), nil
	}
	return toolText(string(b)), nil
}

// approvalAuditOpts builds the store query. Agent and categories are fixed
// here, never taken from input: they are the scope of the view.
func (s *Server) approvalAuditOpts(category, status string, days, limit int) (audit.ListOpts, string) {
	opts := audit.ListOpts{
		Agent:      s.deps.AgentName,
		Categories: approvalAuditCategories,
		Statuses:   audit.ParseFilterList(status),
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

func (s *Server) listApprovalAudit(ctx context.Context, opts audit.ListOpts) (approvalAuditResult, error) {
	events, total, err := s.deps.AuditStore.List(ctx, opts)
	if err != nil {
		return approvalAuditResult{}, err
	}
	out := approvalAuditResult{Events: make([]approvalAuditEvent, 0, len(events)), Total: total}
	for i := range events {
		out.Events = append(out.Events, compactAuditEvent(&events[i]))
	}
	return out, nil
}

// searchApprovalAudit matches search against the redacted summary, never the
// stored one. A store-side LIKE would let "would DENY" pick out the calls a
// shadow decider would have denied. It scans the newest approvalAuditScanMax
// events, so total counts matches within that window.
func (s *Server) searchApprovalAudit(ctx context.Context, opts audit.ListOpts, search string) (approvalAuditResult, error) {
	limit := opts.Limit
	needle := strings.ToLower(search)
	out := approvalAuditResult{Events: []approvalAuditEvent{}}
	opts.Limit = approvalAuditScanPage
	for opts.Offset = 0; opts.Offset < approvalAuditScanMax; opts.Offset += approvalAuditScanPage {
		events, _, err := s.deps.AuditStore.List(ctx, opts)
		if err != nil {
			return approvalAuditResult{}, err
		}
		for i := range events {
			ev := compactAuditEvent(&events[i])
			if !strings.Contains(strings.ToLower(ev.Summary), needle) {
				continue
			}
			out.Total++
			if len(out.Events) < limit {
				out.Events = append(out.Events, ev)
			}
		}
		if len(events) < approvalAuditScanPage {
			break
		}
	}
	return out, nil
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
		redactShadow(&out, nil)
		return out
	}
	redactShadow(&out, detail)
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

// redactShadow hides a shadow decider review's verdict and reasoning. The
// summary reads "SHADOW would DENY <tool>: <reason>", so it is replaced, and
// detail.reason is dropped. It edits detail in place.
func redactShadow(out *approvalAuditEvent, detail map[string]any) {
	if detail["decision"] != "shadow" && !strings.HasPrefix(out.Summary, "SHADOW") {
		return
	}
	out.Summary = "SHADOW review"
	if tool, ok := detail["tool"].(string); ok {
		out.Summary = "SHADOW review of " + truncateUTF8(tool, approvalAuditSummaryBytes)
	}
	delete(detail, "reason")
}
