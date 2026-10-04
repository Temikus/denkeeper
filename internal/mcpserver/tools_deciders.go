package mcpserver

import (
	"context"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	deciderReviewsDefaultLimit = 50
	deciderReviewsMaxLimit     = 200
)

type deciderReviewsInput struct {
	Agent   string `json:"agent" jsonschema:"Agent whose tool calls were reviewed"`
	Decider string `json:"decider,omitempty" jsonschema:"[[llm.deciders]] name (default: the agent's supervisor_decider)"`
	Since   string `json:"since,omitempty" jsonschema:"Start of the window, RFC 3339 (default: 30 days ago)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Max reviews returned, newest first (default 50, max 200)"`
}

// deciderReviewsOutput is the REST decider-reviews body plus total, since
// limit can cut the list.
type deciderReviewsOutput struct {
	Agent      string               `json:"agent"`
	Decider    string               `json:"decider"`
	Supervisor string               `json:"supervisor,omitempty"`
	Since      time.Time            `json:"since"`
	Until      time.Time            `json:"until"`
	Truncated  bool                 `json:"truncated"` // the scan cap was hit; the oldest reviews are missing
	Failed     int                  `json:"failed"`
	Total      int                  `json:"total"` // reviews in the window before limit
	Reviews    []agent.ShadowReview `json:"reviews"`
}

func (s *Server) registerDeciderTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "decider_reviews",
		Description: "List an agent's shadow-mode decision model reviews, newest first, each " +
			"paired with the supervisor's verdict on the same tool call. Each review has " +
			"per-check scores, the lowest one, and both stages' costs. Use it to judge " +
			"whether the decider's approve_at/deny_at thresholds would have agreed with " +
			"the supervisor. Enforce-mode reviews are left out. 'total' counts reviews " +
			"before 'limit' (default 50, max 200). Requires 'audit:read' scope.",
	}, s.handleDeciderReviews)
}

func (s *Server) handleDeciderReviews(ctx context.Context, _ *mcp.CallToolRequest, input deciderReviewsInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "audit:read"); err != nil {
		return err, nil, nil
	}
	if s.deps.AuditStore == nil {
		return toolError("audit not configured"), nil, nil
	}
	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}
	limit := input.Limit
	switch {
	case limit < 0:
		return toolError("invalid limit: must be positive"), nil, nil
	case limit == 0:
		limit = deciderReviewsDefaultLimit
	case limit > deciderReviewsMaxLimit:
		limit = deciderReviewsMaxLimit
	}

	until := time.Now().UTC()
	since := until.Add(-agent.DefaultShadowReviewWindow)
	if input.Since != "" {
		t, err := time.Parse(time.RFC3339, input.Since)
		if err != nil {
			return toolError("invalid since: " + err.Error()), nil, nil
		}
		since = t.UTC() // the store compares timestamps as UTC strings
	}

	decider := s.reviewedDecider(e, input.Decider)
	if decider == "" {
		return toolError("agent has no supervisor_decider; pass 'decider'"), nil, nil
	}

	set, err := agent.LoadShadowReviews(ctx, s.deps.AuditStore, e.Name(), decider, since, until)
	if err != nil {
		return toolError("listing decider reviews: " + err.Error()), nil, nil
	}
	out := deciderReviewsOutput{
		Agent: e.Name(), Decider: decider, Supervisor: supervisorName(e),
		Since: since, Until: until, Truncated: set.Truncated, Failed: set.Failed,
		Total: len(set.Reviews), Reviews: set.Reviews[:min(limit, len(set.Reviews))],
	}
	r, jsonErr := toolJSON(out)
	return r, nil, jsonErr
}

// reviewedDecider picks the decider to report on in the same order as the REST
// handler: the requested one, else the configured one, else the wired one.
func (s *Server) reviewedDecider(e *agent.Engine, requested string) string {
	if requested != "" {
		return requested
	}
	if cfg := s.deps.Config.Get(); cfg != nil {
		for _, ac := range cfg.Agents {
			if ac.Name == e.Name() && ac.SupervisorDecider != "" {
				return ac.SupervisorDecider
			}
		}
	}
	if d := e.SupervisorDecider(); d != nil {
		return d.Name()
	}
	return ""
}
