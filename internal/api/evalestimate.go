package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/Temikus/denkeeper/internal/llm"
)

// evalEstimateInput mirrors evalRunInput's shape so the launcher prices exactly
// the run it is about to start.
type evalEstimateInput struct {
	TaskSet   string             `json:"task_set"`
	BaseAgent string             `json:"base_agent"`
	Variants  []evalVariantInput `json:"variants"`
	// K is samples per (task, variant). Omitted uses [eval] default_k.
	K int `json:"k"`
	// SampleTasks is the Quick check subset size. Omitted or >= the set size
	// prices the whole set.
	SampleTasks int `json:"sample_tasks"`
}

// evalConfigResponse is the read-only [eval] policy. It exists so the launcher
// and the results view show the defaults and thresholds the server actually
// judges against instead of hardcoding a copy. Deliberately read-only:
// runtime-editable thresholds were dropped from v1, so there is no writer.
type evalConfigResponse struct {
	DefaultK           int     `json:"default_k"`
	MaxCostPerRun      float64 `json:"max_cost_per_run"`
	MaxConcurrent      int     `json:"max_concurrent"`
	CompletenessFloor  float64 `json:"completeness_floor"`
	WinThreshold       float64 `json:"win_threshold"`
	GateRejectedRatePP float64 `json:"gate_rejected_rate_pp"`
	GateRoundsPct      float64 `json:"gate_rounds_pct"`
	GateCostPct        float64 `json:"gate_cost_pct"`
	Audit              string  `json:"audit"`
	// JudgeModel is the internal judge's model, omitted when [eval] judge_model
	// is unset. Its presence is the signal a results view uses to decide
	// whether server-side judging is on offer at all — without it the only
	// judge path is Claude Code over MCP.
	JudgeModel string `json:"judge_model,omitempty"`
	// JudgeMaxCostPerRun is the ceiling for one judging pass, so a page can
	// say what it is about to authorise. Omitted with the model.
	JudgeMaxCostPerRun float64 `json:"judge_max_cost_per_run,omitempty"`
	// RubricVersion names the rubric revision the internal judge grades under.
	RubricVersion string `json:"rubric_version,omitempty"`
}

// handleEvalEstimate godoc
// @Summary Estimate what an eval run will cost
// @Description Prices a run before it is created, so the launcher can show a range beside the hard cap. Per (task, variant) the basis is, in order: history (the task's source conversation has real telemetry, giving an honest per-exchange average — scaled by the list-price ratio when the variant runs a different model), list price (the variant's advertised per-million-token price against a nominal per-turn token budget), or unknown. Nothing is fabricated: a variant that can be priced neither way reports basis "unknown" with a zero range, and the caller shows the cap alone. When sample_tasks narrows the run the drawn subset is not known yet, so the figure scales the mean per-task cost and says so in note.
// @Tags eval
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body evalEstimateInput true "Task set, base agent and the variants to price"
// @Success 200 {object} eval.Estimate "Cost range in USD with a per-variant breakdown"
// @Failure 400 {object} map[string]string "Invalid JSON, no variants, or unknown agent"
// @Failure 404 {object} map[string]string "Task set not found"
// @Failure 500 {object} map[string]string "Store error"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/estimate [post]
func (s *Server) handleEvalEstimate(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	var input evalEstimateInput
	if !decodeEvalBody(w, r, &input) {
		return
	}
	if len(input.Variants) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one variant is required"})
		return
	}

	set, err := s.deps.EvalStore.GetTaskSet(r.Context(), input.TaskSet)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	tasks, err := s.deps.EvalStore.ListTasks(r.Context(), set.ID)
	if err != nil {
		writeEvalError(w, err)
		return
	}

	e := s.deps.Dispatcher.Agent(input.BaseAgent)
	if e == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("agent %q not found", input.BaseAgent)})
		return
	}

	k := input.K
	if k <= 0 {
		k = s.deps.EvalRunner.Config().DefaultK
	}

	// Model and provider names are taken at face value here, unlike run
	// creation: an estimate spends nothing, and an unknown name is already
	// visible as an "unknown" basis rather than a rejection.
	variants := make([]eval.EstimateVariant, 0, len(input.Variants))
	for _, v := range input.Variants {
		variants = append(variants, eval.EstimateVariant{
			Name:     strings.TrimSpace(v.Name),
			Model:    v.LLMModel,
			Provider: v.LLMProvider,
		})
	}

	est, err := s.evalEstimator().Estimate(r.Context(), eval.EstimateInput{
		Tasks:        tasks,
		Variants:     variants,
		K:            k,
		SampleTasks:  input.SampleTasks,
		BaseModel:    e.ModelName(),
		BaseProvider: e.ProviderName(),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, est)
}

// evalEstimator wires the two lookups the estimator needs. Either may be nil,
// which removes the basis it serves rather than failing the request.
func (s *Server) evalEstimator() eval.Estimator {
	var est eval.Estimator
	// conversation_stats lives on TelemetryStore, obtained by type assertion —
	// a store without it simply has no history basis to offer.
	if tel, ok := s.deps.Memory.(agent.TelemetryStore); ok {
		est.Stats = evalStatsLookup{mem: tel}
	}
	if s.deps.ModelDetailLister != nil {
		est.Prices = &evalPriceLookup{list: s.deps.ModelDetailLister, cache: map[string]map[string]eval.ModelPrice{}}
	}
	return est
}

// evalStatsLookup adapts the memory store to the estimator's history basis.
type evalStatsLookup struct{ mem agent.TelemetryStore }

func (l evalStatsLookup) ConversationStats(ctx context.Context, convID string) (*eval.ConvStats, error) {
	row, err := l.mem.GetConversationStats(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("conversation stats: %w", err)
	}
	if row == nil {
		return nil, nil
	}
	return &eval.ConvStats{TotalCost: row.TotalCost, TotalMessages: row.TotalMessages}, nil
}

// evalPriceLookup adapts ModelDetailLister to the estimator's list-price basis.
// It caches one listing per provider for the life of the request: the lister
// can be a network round-trip and a run prices the same two models against
// every task in the set.
//
// Not safe for concurrent use — one instance serves one request.
type evalPriceLookup struct {
	list  func(ctx context.Context, providerFilter string) []llm.ModelInfo
	cache map[string]map[string]eval.ModelPrice
}

func (l *evalPriceLookup) ModelPrice(ctx context.Context, provider, model string) (eval.ModelPrice, bool) {
	byModel, cached := l.cache[provider]
	if !cached {
		lctx, cancel := context.WithTimeout(ctx, modelValidationTimeout)
		defer cancel()
		byModel = make(map[string]eval.ModelPrice)
		for _, m := range l.list(lctx, provider) {
			var p eval.ModelPrice
			if m.InputPerMTok != nil {
				p.InputPerMTok = *m.InputPerMTok
			}
			if m.OutputPerMTok != nil {
				p.OutputPerMTok = *m.OutputPerMTok
			}
			byModel[m.ID] = p
		}
		l.cache[provider] = byModel
	}
	p, ok := byModel[model]
	return p, ok
}

// handleEvalConfig godoc
// @Summary Get the eval subsystem's configured defaults and thresholds
// @Description Returns the [eval] TOML section: the run defaults a launcher should preselect (default_k, max_cost_per_run, max_concurrent) and the policy a scorecard is judged against (completeness_floor, win_threshold, the three objective gates). Read-only by design — v1 keeps threshold edits in TOML plus a reload, and every verdict already carries the thresholds it was measured against.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Success 200 {object} evalConfigResponse "Eval defaults and thresholds"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/config [get]
func (s *Server) handleEvalConfig(w http.ResponseWriter, _ *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	if s.deps.Config == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server config not available"})
		return
	}
	c := s.deps.Config.Eval
	out := evalConfigResponse{
		DefaultK:           c.DefaultK,
		MaxCostPerRun:      c.MaxCostPerRun,
		MaxConcurrent:      c.MaxConcurrent,
		CompletenessFloor:  c.CompletenessFloor,
		WinThreshold:       c.WinThreshold,
		GateRejectedRatePP: c.GateRejectedRatePP,
		GateRoundsPct:      c.GateRoundsPct,
		GateCostPct:        c.GateCostPct,
		Audit:              c.AuditMode(),
	}
	// Reported from the judge's own resolved config rather than from the TOML
	// struct: a judge the server never wired must not be advertised.
	if s.deps.EvalJudge.Available() {
		jc := s.deps.EvalJudge.Config()
		out.JudgeModel = jc.Model
		out.JudgeMaxCostPerRun = jc.MaxCost
		out.RubricVersion = eval.RubricVersion
	}
	writeJSON(w, http.StatusOK, out)
}
