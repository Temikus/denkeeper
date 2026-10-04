package api

import (
	"net/http"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
)

// deciderReviewsResponse is the body of GET /agents/{name}/decider-reviews.
type deciderReviewsResponse struct {
	Agent      string               `json:"agent"`
	Decider    string               `json:"decider"`
	Supervisor string               `json:"supervisor"` // the agent's configured supervisor, "" when none
	Since      time.Time            `json:"since"`
	Until      time.Time            `json:"until"`
	Truncated  bool                 `json:"truncated"` // the scan cap was hit; the oldest reviews are missing
	Failed     int                  `json:"failed"`    // decider reviews that errored in the window
	Reviews    []agent.ShadowReview `json:"reviews"`
}

// handleDeciderReviews godoc
// @Summary List an agent's shadow decider reviews
// @Description Returns the agent's shadow-mode decision model reviews from the audit log, newest first, each paired with the supervisor's verdict on the same tool call (matched by conversation, tool and arguments). Each review carries the per-check scores, the lowest one, and both stages' costs; supervisor_cost is null for reviews audited before it was recorded. Enforce-mode reviews are left out, since the supervisor sees only the escalated calls there. Read-only: nothing is re-scored.
// @Tags agents
// @Produce json
// @Security BearerAuth
// @Param name path string true "Agent name"
// @Param decider query string false "[[llm.deciders]] name (default: the agent's supervisor_decider)"
// @Param since query string false "Start of the window, RFC3339 (default: 30 days ago)"
// @Success 200 {object} deciderReviewsResponse
// @Failure 400 {object} map[string]string "Invalid since, or no decider named and none set on the agent"
// @Failure 404 {object} map[string]string "Agent not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "Audit not configured"
// @Router /agents/{name}/decider-reviews [get]
func (s *Server) handleDeciderReviews(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	e := s.deps.Dispatcher.Agent(name)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	if s.deps.AuditStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit not configured"})
		return
	}

	until := time.Now().UTC()
	since := until.Add(-agent.DefaultShadowReviewWindow)
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since: must be RFC3339"})
			return
		}
		since = t.UTC() // the store compares timestamps as UTC strings
	}

	supervisor, decider := s.reviewedDecider(e, name, r.URL.Query().Get("decider"))
	if decider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent has no supervisor_decider; pass ?decider="})
		return
	}

	set, err := agent.LoadShadowReviews(r.Context(), s.deps.AuditStore, name, decider, since, until)
	if err != nil {
		s.logger.Error("listing decider reviews", "agent", name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, deciderReviewsResponse{
		Agent: name, Decider: decider, Supervisor: supervisor,
		Since: since, Until: until, Truncated: set.Truncated, Failed: set.Failed, Reviews: set.Reviews,
	})
}

// reviewedDecider returns the agent's configured supervisor and the decider to
// report on: the requested one, else the configured one, else the wired one.
func (s *Server) reviewedDecider(e *agent.Engine, name, requested string) (supervisor, decider string) {
	decider = requested
	if cfg := s.appConfig(); cfg != nil {
		for _, ac := range cfg.Agents {
			if ac.Name == name {
				supervisor = ac.Supervisor
				if decider == "" {
					decider = ac.SupervisorDecider
				}
			}
		}
	}
	if d := e.SupervisorDecider(); decider == "" && d != nil {
		decider = d.Name()
	}
	return supervisor, decider
}
