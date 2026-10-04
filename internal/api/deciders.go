package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
)

// DeciderRuntime applies decider changes to the running process.
type DeciderRuntime interface {
	// Apply rebuilds the live decider set from snap and rebinds every
	// consumer: supervisor stages, the eval judge and the decide tool.
	Apply(snap *config.Config)
	// Test asks dc one question through the live provider set. dc need not
	// be configured, so a decider can be tried before it is added.
	Test(ctx context.Context, dc config.DeciderConfig) (*llm.DecisionResponse, error)
}

// deciderTestTimeout bounds POST /llm/deciders/test, like the provider probe.
const deciderTestTimeout = 10 * time.Second

// deciderInput is the body of POST /llm/deciders and PATCH /llm/deciders/{name}.
// On PATCH, an omitted field is unchanged; "" or 0 restores the default.
type deciderInput struct {
	Name           *string `json:"name,omitempty"`
	Provider       *string `json:"provider,omitempty"`
	Model          *string `json:"model,omitempty"`
	Timeout        *string `json:"timeout,omitempty"`          // Go duration; "" = default 5s
	MaxInputTokens *int    `json:"max_input_tokens,omitempty"` // 0 = default 30000
}

// deciderTestInput names a configured decider, or describes an unsaved one.
type deciderTestInput struct {
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
}

type deciderTestResponse struct {
	Status    string  `json:"status"` // "ok" or "error"
	Message   string  `json:"message"`
	Model     string  `json:"model,omitempty"`
	LatencyMs int64   `json:"latency_ms"`
	CostUSD   float64 `json:"cost_usd"`
}

type deciderMutationResponse struct {
	Decider         deciderInfo `json:"decider"`
	RestartRequired bool        `json:"restart_required"`
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func findDecider(cfg *config.Config, name string) (config.DeciderConfig, bool) {
	i := slices.IndexFunc(cfg.LLM.Deciders, func(d config.DeciderConfig) bool { return d.Name == name })
	if i < 0 {
		return config.DeciderConfig{}, false
	}
	return cfg.LLM.Deciders[i], true
}

func newDeciderInfo(cfg *config.Config, dc config.DeciderConfig) deciderInfo {
	usedBy := config.DeciderReferrers(cfg, dc.Name)
	if usedBy == nil {
		usedBy = []string{}
	}
	return deciderInfo{
		Name:           dc.Name,
		Provider:       dc.Provider,
		Model:          dc.Model,
		Timeout:        dc.Timeout,
		MaxInputTokens: dc.MaxInputTokens,
		UsedBy:         usedBy,
	}
}

// applyDecidersLive pushes snap to the running deciders. It reports false when
// no runtime is wired, so the change waits for a restart.
func (s *Server) applyDecidersLive(snap *config.Config) bool {
	if s.deps.DeciderRuntime == nil || snap == nil {
		return false
	}
	s.deps.DeciderRuntime.Apply(snap)
	return true
}

func (s *Server) auditDecider(ctx context.Context, action, summary string) {
	if s.deps.Auditor == nil {
		return
	}
	s.deps.Auditor.Emit(ctx, audit.Event{
		Category: audit.CategoryConfig,
		Action:   action,
		Summary:  summary,
		Status:   audit.StatusOK,
		Source:   "api",
	})
}

// requireConfigPath writes a 503 and returns false when there is no config
// file to persist to.
func (s *Server) requireConfigPath(w http.ResponseWriter) bool {
	if s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config persistence not available"})
		return false
	}
	return true
}

func decodeDeciderInput(w http.ResponseWriter, r *http.Request) (deciderInput, bool) {
	var input deciderInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return input, false
	}
	return input, true
}

// handleCreateDecider godoc
// @Summary Create decision model
// @Description Adds a [[llm.deciders]] entry, persists it to TOML and makes it usable without a restart. Only provider types that serve decisions (openrouter) are accepted.
// @Tags providers
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body deciderInput true "Decision model"
// @Success 201 {object} deciderMutationResponse
// @Failure 400 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /llm/deciders [post]
func (s *Server) handleCreateDecider(w http.ResponseWriter, r *http.Request) {
	if !s.requireConfigPath(w) {
		return
	}
	input, ok := decodeDeciderInput(w, r)
	if !ok {
		return
	}
	dc := config.DeciderConfig{
		Name:     derefString(input.Name),
		Provider: derefString(input.Provider),
		Model:    derefString(input.Model),
		Timeout:  derefString(input.Timeout),
	}
	if input.MaxInputTokens != nil {
		dc.MaxInputTokens = *input.MaxInputTokens
	}

	snap := s.appConfig()
	if status, msg := validateDeciderCreate(snap, dc); status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	if err := config.AddDeciderToConfig(s.deps.ConfigPath, dc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persisting decider to config: " + err.Error()})
		return
	}

	config.ApplyDeciderDefaults(&dc)
	updated := s.deps.Config.Update(func(c *config.Config) { c.LLM.Deciders = append(c.LLM.Deciders, dc) })
	live := s.applyDecidersLive(updated)
	s.auditDecider(r.Context(), "create_decider", "Created decision model "+dc.Name+" ("+dc.Model+" via "+dc.Provider+")")
	writeJSON(w, http.StatusCreated, deciderMutationResponse{Decider: newDeciderInfo(updated, dc), RestartRequired: !live})
}

func validateDeciderCreate(snap *config.Config, dc config.DeciderConfig) (int, string) {
	if !config.ValidResourceName(dc.Name) {
		return http.StatusBadRequest, "invalid name: lowercase alphanumeric with hyphens, 1-64 chars"
	}
	if _, exists := findDecider(snap, dc.Name); exists {
		return http.StatusConflict, "decision model already exists: " + dc.Name
	}
	if err := config.ValidateDecider(snap, dc); err != nil {
		return http.StatusBadRequest, err.Error()
	}
	return 0, ""
}

// handlePatchDecider godoc
// @Summary Update decision model
// @Description Changes a decision model's provider, model, timeout or max_input_tokens and applies it without a restart. An empty timeout or a zero max_input_tokens restores the default. Decision models cannot be renamed.
// @Tags providers
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Decision model name"
// @Param body body deciderInput true "Fields to change"
// @Success 200 {object} deciderMutationResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /llm/deciders/{name} [patch]
func (s *Server) handlePatchDecider(w http.ResponseWriter, r *http.Request) {
	if !s.requireConfigPath(w) {
		return
	}
	name := r.PathValue("name")
	input, ok := decodeDeciderInput(w, r)
	if !ok {
		return
	}
	if input.Name != nil && *input.Name != name {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "decision models cannot be renamed; add a new one and delete this one"})
		return
	}
	snap := s.appConfig()
	cur, exists := findDecider(snap, name)
	if !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "decision model not found: " + name})
		return
	}
	merged, changes := mergeDeciderInput(cur, input)
	if err := config.ValidateDecider(snap, merged); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := config.UpdateDeciderConfig(s.deps.ConfigPath, name, changes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "persisting decider to config: " + err.Error()})
		return
	}

	config.ApplyDeciderDefaults(&merged)
	updated := s.deps.Config.Update(func(c *config.Config) {
		for i := range c.LLM.Deciders {
			if c.LLM.Deciders[i].Name == name {
				c.LLM.Deciders[i] = merged
			}
		}
	})
	live := s.applyDecidersLive(updated)
	s.auditDecider(r.Context(), "update_decider", "Updated decision model "+name+" ("+merged.Model+" via "+merged.Provider+")")
	writeJSON(w, http.StatusOK, deciderMutationResponse{Decider: newDeciderInfo(updated, merged), RestartRequired: !live})
}

// mergeDeciderInput applies a PATCH body to cur. changes is the TOML edit:
// a nil value deletes the key so its default applies.
func mergeDeciderInput(cur config.DeciderConfig, input deciderInput) (config.DeciderConfig, map[string]any) {
	merged := cur
	changes := map[string]any{}
	if input.Provider != nil {
		merged.Provider = *input.Provider
		changes["provider"] = *input.Provider
	}
	if input.Model != nil {
		merged.Model = *input.Model
		changes["model"] = *input.Model
	}
	if input.Timeout != nil {
		merged.Timeout = *input.Timeout
		changes["timeout"] = nilIfZero(*input.Timeout)
	}
	if input.MaxInputTokens != nil {
		merged.MaxInputTokens = *input.MaxInputTokens
		changes["max_input_tokens"] = nilIfZero(int64(*input.MaxInputTokens))
	}
	return merged, changes
}

func nilIfZero[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

// handleDeleteDecider godoc
// @Summary Delete decision model
// @Description Removes a decision model. Rejected with 409 and a used_by list while an agent, the eval judge or the decide tool uses it.
// @Tags providers
// @Produce json
// @Security BearerAuth
// @Param name path string true "Decision model name"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]any
// @Failure 503 {object} map[string]string
// @Router /llm/deciders/{name} [delete]
func (s *Server) handleDeleteDecider(w http.ResponseWriter, r *http.Request) {
	if !s.requireConfigPath(w) {
		return
	}
	name := r.PathValue("name")
	snap := s.appConfig()
	if _, exists := findDecider(snap, name); !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "decision model not found: " + name})
		return
	}
	if refs := config.DeciderReferrers(snap, name); len(refs) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "decision model is in use: " + name, "used_by": refs})
		return
	}
	if err := config.RemoveDeciderFromConfig(s.deps.ConfigPath, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "removing decider from config: " + err.Error()})
		return
	}
	updated := s.deps.Config.Update(func(c *config.Config) {
		c.LLM.Deciders = slices.DeleteFunc(c.LLM.Deciders, func(d config.DeciderConfig) bool { return d.Name == name })
	})
	s.applyDecidersLive(updated)
	s.auditDecider(r.Context(), "delete_decider", "Deleted decision model "+name)
	w.WriteHeader(http.StatusNoContent)
}

// handleTestDecider godoc
// @Summary Test a decision model
// @Description Asks one trivial question through a configured decision model (by name) or an unsaved provider and model, and reports latency and cost. A failed call is a 200 with status "error". The call is billed by the provider but not to any agent.
// @Tags providers
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body deciderTestInput true "Decision model to test"
// @Success 200 {object} deciderTestResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /llm/deciders/test [post]
func (s *Server) handleTestDecider(w http.ResponseWriter, r *http.Request) {
	if s.deps.DeciderRuntime == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "decision models are not available"})
		return
	}
	var input deciderTestInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	dc, status, msg := resolveDeciderTest(s.appConfig(), input)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), deciderTestTimeout)
	defer cancel()
	start := time.Now()
	resp, err := s.deps.DeciderRuntime.Test(ctx, dc)
	out := deciderTestResponse{LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		out.Status, out.Message = "error", deciderTestMessage(err)
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Status, out.Message, out.Model, out.CostUSD = "ok", "Decision model answered", resp.Model, resp.CostUSD
	writeJSON(w, http.StatusOK, out)
}

// resolveDeciderTest turns a test body into the decider to call: the stored
// entry for a name, or the given provider and model checked as a create would.
func resolveDeciderTest(snap *config.Config, input deciderTestInput) (config.DeciderConfig, int, string) {
	if input.Name != "" {
		dc, ok := findDecider(snap, input.Name)
		if !ok {
			return dc, http.StatusNotFound, "decision model not found: " + input.Name
		}
		return dc, 0, ""
	}
	dc := config.DeciderConfig{Name: "test", Provider: input.Provider, Model: input.Model, Timeout: input.Timeout}
	if err := config.ValidateDecider(snap, dc); err != nil {
		return dc, http.StatusBadRequest, err.Error()
	}
	config.ApplyDeciderDefaults(&dc)
	return dc, 0, ""
}

func deciderTestMessage(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, llm.ErrNoDecisionProvider):
		return "the provider is not running or does not serve decisions"
	}
	return err.Error()
}
