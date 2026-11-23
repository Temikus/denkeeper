package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
)

// deciderCountTTL is how long a per-agent unreviewed count is reused. The nav
// badge polls every 15 s; a mark clears the agent's entry at once.
const deciderCountTTL = 60 * time.Second

// deciderCountEntry is one memoized count, valid for its decider and thresholds.
type deciderCountEntry struct {
	decider           string
	approveAt, denyAt float64
	count             int
	at                time.Time
}

// deciderReviewMarkResponse is the stored review mark.
type deciderReviewMarkResponse struct {
	Agent        string    `json:"agent"`
	Decider      string    `json:"decider"`
	ReviewedUpTo time.Time `json:"reviewed_up_to"`
	ReviewedBy   string    `json:"reviewed_by"`
}

// handleDeciderReviewCounts godoc
// @Summary Count unreviewed unsafe approvals per agent
// @Description For every running agent with a shadow-mode decider, the number of shadow reviews newer than the agent's review mark where the decider would approve at its current thresholds and the supervisor denied or escalated. Agents without a shadow decider are left out; zero is included. Looks back at most 30 days. Counts are reused for up to 60 seconds per agent, and marking an agent reviewed clears its count at once.
// @Tags agents
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]int "Agent name to count"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "Audit not configured"
// @Router /decider-reviews/unreviewed [get]
func (s *Server) handleDeciderReviewCounts(w http.ResponseWriter, r *http.Request) {
	if s.deps.AuditStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit not configured"})
		return
	}
	out := map[string]int{}
	for _, name := range s.deps.Dispatcher.Agents() {
		e := s.deps.Dispatcher.Agent(name)
		if e == nil {
			continue
		}
		d := e.SupervisorDecider()
		cfg, ok := e.SupervisorDeciderConfig()
		if d == nil || !ok || cfg.Mode == agent.DeciderModeEnforce {
			continue
		}
		n, err := s.unreviewedCount(r.Context(), name, d.Name(), cfg)
		if err != nil {
			s.logger.Error("counting unreviewed decider reviews", "agent", name, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		out[name] = n
	}
	writeJSON(w, http.StatusOK, out)
}

// unreviewedCount returns the memoized count while it is fresh and was taken
// for the same decider and thresholds, else counts again.
func (s *Server) unreviewedCount(ctx context.Context, name, decider string, cfg agent.DeciderStageConfig) (int, error) {
	s.deciderCountMu.Lock()
	c, ok := s.deciderCounts[name]
	s.deciderCountMu.Unlock()
	if ok && c.decider == decider && c.approveAt == cfg.ApproveAt && c.denyAt == cfg.DenyAt && time.Since(c.at) < deciderCountTTL {
		return c.count, nil
	}
	mark, _, err := s.deps.AuditStore.ReviewMark(ctx, name, decider)
	if err != nil {
		return 0, err
	}
	n, err := agent.CountUnsafeApprovals(ctx, s.deps.AuditStore, name, decider, mark.ReviewedUpTo, cfg.ApproveAt, cfg.DenyAt)
	if err != nil {
		return 0, err
	}
	s.deciderCountMu.Lock()
	if s.deciderCounts == nil {
		s.deciderCounts = map[string]deciderCountEntry{}
	}
	s.deciderCounts[name] = deciderCountEntry{decider: decider, approveAt: cfg.ApproveAt, denyAt: cfg.DenyAt, count: n, at: time.Now()}
	s.deciderCountMu.Unlock()
	return n, nil
}

type markReviewedInput struct {
	Decider string `json:"decider"` // optional: defaults as for the reviews list
	UpTo    string `json:"up_to"`   // RFC3339; the newest review time the client saw
}

// handleMarkDeciderReviewed godoc
// @Summary Mark an agent's shadow decider reviews as reviewed
// @Description Moves the agent's review mark forward to up_to, the time of the newest review the caller has seen, and records the caller's key or login name. The mark never moves back: an older up_to leaves the stored mark in place, and the response is the stored mark. Reviews at or before the mark no longer count toward the unreviewed badge.
// @Tags agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Agent name"
// @Param body body markReviewedInput true "Decider (optional) and up_to (RFC3339)"
// @Success 200 {object} deciderReviewMarkResponse
// @Failure 400 {object} map[string]string "Missing, invalid or future up_to, or no decider named and none set on the agent"
// @Failure 404 {object} map[string]string "Agent not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "Audit not configured"
// @Router /agents/{name}/decider-reviews/reviewed [post]
func (s *Server) handleMarkDeciderReviewed(w http.ResponseWriter, r *http.Request) {
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
	var in markReviewedInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	upTo, msg := parseUpTo(in.UpTo)
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	_, decider := s.reviewedDecider(e, name, in.Decider)
	if decider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent has no supervisor_decider; pass decider"})
		return
	}
	m, err := s.deps.AuditStore.SetReviewMark(r.Context(), audit.ReviewMark{
		Agent: name, Decider: decider, ReviewedUpTo: upTo, ReviewedBy: requestKeyName(r.Context()),
	})
	if err != nil {
		s.logger.Error("storing decider review mark", "agent", name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	s.deciderCountMu.Lock()
	delete(s.deciderCounts, name)
	s.deciderCountMu.Unlock()
	writeJSON(w, http.StatusOK, deciderReviewMarkResponse{
		Agent: m.Agent, Decider: m.Decider, ReviewedUpTo: m.ReviewedUpTo, ReviewedBy: m.ReviewedBy,
	})
}

// parseUpTo validates up_to and returns an error message when it is bad. No
// client can have seen a review from the future.
func parseUpTo(v string) (time.Time, string) {
	if v == "" {
		return time.Time{}, "up_to is required"
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, "invalid up_to: must be RFC3339"
	}
	if t.After(time.Now()) {
		return time.Time{}, "up_to is in the future"
	}
	return t.UTC(), ""
}
