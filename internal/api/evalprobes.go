package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/eval"
)

// evalProbesResult is the top-down fill path's response: test cases generated
// from the agent's own configured intent rather than mined from its history.
type evalProbesResult struct {
	Agent string `json:"agent"`
	// PermissionTier is echoed because half the probes are written against it,
	// and an operator reading the cards should see which tier they assume.
	PermissionTier string       `json:"permission_tier"`
	Probes         []eval.Probe `json:"probes"`
}

const (
	// probeMaxLimit bounds a generation pass. The generator's own default
	// applies when the caller does not ask for one.
	probeMaxLimit = 100
)

// autoApproveTools returns the tool names this agent may run unattended:
// TOML-declared config rules plus persisted permanent ones. Session rules are
// deliberately excluded — they expire, and a probe written around a rule that
// vanishes in fifteen minutes would grade a policy the agent no longer has.
func (s *Server) autoApproveTools(ctx context.Context, agentName string) []string {
	if s.deps.Approvals == nil {
		return nil
	}
	tools := s.deps.Approvals.ConfigRuleTools(agentName)
	rules, err := s.deps.Approvals.ListAutoApproveRules(ctx, agentName)
	if err != nil {
		// A store hiccup here would only widen the probe set (an already-
		// blessed tool getting probed as if it were not), so it is logged and
		// the pass continues rather than failing the request.
		s.logger.Warn("listing auto-approve rules for probe generation", "error", err, "agent", agentName)
		return tools
	}
	for _, r := range rules {
		if r.Scope == approval.ScopePermanent {
			tools = append(tools, r.ToolName)
		}
	}
	return tools
}

// handleEvalProbes godoc
// @Summary Generate spec-derived behavioural probes
// @Description Generates eval test cases top-down from the agent's own written intent — its permission tier, auto-approve policy, persona sections and skill frontmatter — rather than from usage history. Covers what a history-sampled set structurally cannot: denial compliance, permission-tier boundaries, and budget hints are behaviours a well-behaved incumbent never produced, so no past turn demonstrates them. Every probe carries free-text notes describing what a good answer looks like, surfaced to the judge as context and never parsed as assertions. Generation is deterministic for a given agent, so passing set= suppresses probes that set already carries. Nothing is written — accepting a probe is a separate call to the task create endpoint.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param agent query string true "Agent whose configuration is read as the spec"
// @Param set query string false "Task set to exclude probes already saved in"
// @Param limit query int false "Probes returned across all families (max 100)"
// @Success 200 {object} evalProbesResult "Generated probes"
// @Failure 400 {object} map[string]string "Missing agent or bad limit"
// @Failure 404 {object} map[string]string "Agent or task set not found"
// @Failure 500 {object} map[string]string "Store error"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/probes [get]
func (s *Server) handleEvalProbes(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	agentName := r.URL.Query().Get("agent")
	if agentName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent is required: probes are generated from one agent's configuration"})
		return
	}
	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent " + agentName + " not found"})
		return
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return
		}
		limit = min(n, probeMaxLimit)
	}

	exclude, ok := s.probeExclusions(w, r)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, evalProbesResult{
		Agent:          e.Name(),
		PermissionTier: e.PermissionTier(),
		Probes: eval.GenerateProbes(e, eval.ProbeOpts{
			AutoApproveTools: s.autoApproveTools(r.Context(), agentName),
			Limit:            limit,
			Exclude:          exclude,
		}),
	})
}

// probeExclusions reads the optional target set's existing prompts, so a
// second pass against a set that already carries probes offers only what is
// new. Writes the response and reports false when the named set cannot be
// read; an absent set= is not an error and excludes nothing.
func (s *Server) probeExclusions(w http.ResponseWriter, r *http.Request) (map[string]struct{}, bool) {
	name := r.URL.Query().Get("set")
	if name == "" {
		return nil, true
	}
	set, err := s.deps.EvalStore.GetTaskSet(r.Context(), name)
	if err != nil {
		writeEvalError(w, err)
		return nil, false
	}
	tasks, err := s.deps.EvalStore.ListTasks(r.Context(), set.ID)
	if err != nil {
		writeEvalError(w, err)
		return nil, false
	}
	exclude := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		exclude[t.Prompt] = struct{}{}
	}
	return exclude, true
}
