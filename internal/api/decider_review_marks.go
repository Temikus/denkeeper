package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
)

// deciderCountTTL is how long a per-agent unreviewed count is reused. The nav
// badge polls every 15 s; a mark clears the agent's entry at once.
const deciderCountTTL = 60 * time.Second

// deciderCountScanTimeout bounds one shared count scan. No caller's context
// can cancel it (the first caller hanging up must not fail the others), so
// without a bound an abandoned scan could run on. A minute is many times a
// healthy 30-day scan and four badge polls (15 s): only a stuck store hits it,
// and the error it returns is never cached, so the next poll counts again.
const deciderCountScanTimeout = time.Minute

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

// deciderCountKey names one count: the same agent counted for another decider
// or other thresholds is a different number.
type deciderCountKey struct {
	agent, decider    string
	approveAt, denyAt float64
}

// errDeciderCountFailed is what callers sharing a count get when the scan
// panicked instead of returning.
var errDeciderCountFailed = errors.New("counting unreviewed decider reviews failed")

// deciderCountRun is one count scan in flight. done closes when n and err are set.
type deciderCountRun struct {
	done    chan struct{}
	n       int
	err     error
	waiters int // callers sharing this run, for tests
}

// unreviewedCount returns the memoized count while it is fresh and was taken
// for the same decider and thresholds, else counts again. Concurrent callers
// for the same key share one scan.
func (s *Server) unreviewedCount(ctx context.Context, name, decider string, cfg agent.DeciderStageConfig) (int, error) {
	key := deciderCountKey{agent: name, decider: decider, approveAt: cfg.ApproveAt, denyAt: cfg.DenyAt}
	s.deciderCountMu.Lock()
	c, ok := s.deciderCounts[name]
	if ok && c.decider == decider && c.approveAt == cfg.ApproveAt && c.denyAt == cfg.DenyAt && time.Since(c.at) < deciderCountTTL {
		s.deciderCountMu.Unlock()
		return c.count, nil
	}
	if run, ok := s.deciderCountRuns[key]; ok {
		run.waiters++
		s.deciderCountMu.Unlock()
		select {
		case <-run.done:
			return run.n, run.err
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	run := &deciderCountRun{done: make(chan struct{})}
	if s.deciderCountRuns == nil {
		s.deciderCountRuns = map[deciderCountKey]*deciderCountRun{}
	}
	s.deciderCountRuns[key] = run
	gen := s.deciderCountGen[name]
	s.deciderCountMu.Unlock()

	// If the scan panics, release the run on the way out so waiters get an
	// error and the next count starts afresh; otherwise every later count for
	// this key would join the dead run and wait forever. The panic still
	// propagates after this.
	counted := false
	defer func() {
		if counted {
			return
		}
		s.deciderCountMu.Lock()
		run.err = errDeciderCountFailed
		delete(s.deciderCountRuns, key)
		close(run.done)
		s.deciderCountMu.Unlock()
	}()

	// Shared by every waiter, so the first caller hanging up must not cancel
	// it for the rest; it has its own bound instead.
	timeout := s.deciderCountTimeout
	if timeout <= 0 {
		timeout = deciderCountScanTimeout
	}
	scanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	run.n, run.err = s.countUnreviewed(scanCtx, name, decider, cfg)
	counted = true

	s.deciderCountMu.Lock()
	defer s.deciderCountMu.Unlock()
	delete(s.deciderCountRuns, key)
	close(run.done)
	// A mark that landed while counting makes n stale: answer with it, but do
	// not cache it, or the cleared badge would come back for a whole TTL.
	if run.err != nil || s.deciderCountGen[name] != gen {
		return run.n, run.err
	}
	if s.deciderCounts == nil {
		s.deciderCounts = map[string]deciderCountEntry{}
	}
	s.deciderCounts[name] = deciderCountEntry{decider: decider, approveAt: cfg.ApproveAt, denyAt: cfg.DenyAt, count: run.n, at: time.Now()}
	return run.n, nil
}

func (s *Server) countUnreviewed(ctx context.Context, name, decider string, cfg agent.DeciderStageConfig) (int, error) {
	mark, _, err := s.deps.AuditStore.ReviewMark(ctx, name, decider)
	if err != nil {
		return 0, err
	}
	return agent.CountUnsafeApprovals(ctx, s.deps.AuditStore, name, decider, mark.ReviewedUpTo, cfg.ApproveAt, cfg.DenyAt)
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
// @Failure 400 {object} map[string]string "Missing, invalid, future or out-of-range (before 1677-09-21) up_to, or no decider named and none set on the agent"
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
	if s.deciderCountGen == nil {
		s.deciderCountGen = map[string]uint64{}
	}
	s.deciderCountGen[name]++
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
	if !audit.ReviewMarkInRange(t) {
		return time.Time{}, "up_to is out of range: it must be after 1677-09-21"
	}
	return t.UTC(), ""
}
