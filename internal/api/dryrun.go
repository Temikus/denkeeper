package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

// maxTranscriptFieldLen caps tool arguments and results in a dry-run
// transcript. Results are unbounded in principle (a web_fetch page, a large KV
// value) and the transcript is rendered in a browser panel, so it is trimmed
// here rather than in the record itself.
const maxTranscriptFieldLen = 8 * 1024

// dryRunInput is the request body shared by both dry-run endpoints. Schedules
// derive their own message text, so they only accept AsOf.
type dryRunInput struct {
	// Message is the user text to run. Required for skill dry-runs, ignored
	// for schedules.
	Message string `json:"message"`
	// AsOf pins the clock (RFC3339). Empty means now. Pinning matters because
	// both date-injection points — the scheduled-message header and the
	// "## Current Date" prompt section — feed dated keys the agent writes.
	AsOf string `json:"as_of"`
	// Model runs this preview against a model other than the agent's live one.
	// Empty uses the live model. The override applies to this request only and
	// changes nothing about the agent.
	Model string `json:"model"`
}

// dryRunToolCall is one tool call in a transcript.
type dryRunToolCall struct {
	Tool       string `json:"tool"`
	Server     string `json:"server,omitempty"`
	Round      int    `json:"round"`
	Outcome    string `json:"outcome"`
	Suppressed bool   `json:"suppressed"`
	DurationMs int64  `json:"duration_ms"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Error      string `json:"error,omitempty"`
}

// dryRunTranscript is what both endpoints return: everything the turn did,
// with nothing having been sent, written, or remembered.
type dryRunTranscript struct {
	Agent          string    `json:"agent"`
	ConversationID string    `json:"conversation_id"`
	AsOf           time.Time `json:"as_of"`
	Prompt         string    `json:"prompt"`
	Response       string    `json:"response"`
	Rounds         int       `json:"rounds"`
	DurationMs     int64     `json:"duration_ms"`
	Model          string    `json:"model,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	// RequestedModel is set only when the caller overrode the model, so the UI
	// can mark a transcript as "not your live model" without knowing the agent
	// config. Model is what actually answered; the two can differ if the
	// provider redirected.
	RequestedModel string           `json:"requested_model,omitempty"`
	TokensPrompt   int              `json:"tokens_prompt"`
	TokensTotal    int              `json:"tokens_total"`
	CostUSD        float64          `json:"cost_usd"`
	SuppressedN    int              `json:"suppressed_count"`
	ToolCalls      []dryRunToolCall `json:"tool_calls"`
}

// newDryRunConvID mints the in-flight identity for one preview. It is used for
// cost attribution, audit grouping and log correlation, and is never written
// to the conversations table.
func newDryRunConvID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A collision only muddles audit grouping for concurrent previews;
		// the timestamp keeps identities distinct in practice.
		return fmt.Sprintf("dryrun:%d", time.Now().UnixNano())
	}
	return "dryrun:" + hex.EncodeToString(b[:])
}

// parseDryRunInput decodes the body and resolves the clock. An absent body is
// valid — both endpoints have usable defaults.
func parseDryRunInput(r *http.Request) (dryRunInput, time.Time, error) {
	var input dryRunInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err.Error() != "EOF" {
		return input, time.Time{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if input.AsOf == "" {
		return input, time.Now(), nil
	}
	t, err := time.Parse(time.RFC3339, input.AsOf)
	if err != nil {
		return input, time.Time{}, fmt.Errorf("invalid as_of: must be RFC3339")
	}
	return input, t, nil
}

// truncateField trims a transcript field to the render cap.
func truncateField(s string) (string, bool) {
	if len(s) <= maxTranscriptFieldLen {
		return s, false
	}
	return s[:maxTranscriptFieldLen], true
}

// buildTranscript converts an engine turn result into the API shape.
func buildTranscript(agentName string, result *agent.TurnResult) dryRunTranscript {
	t := dryRunTranscript{
		Agent:          agentName,
		ConversationID: result.ConversationID,
		AsOf:           result.AsOf,
		Prompt:         result.Prompt,
		Response:       result.Response,
		Rounds:         result.Rounds,
		DurationMs:     result.DurationMs,
		Model:          result.Model,
		Provider:       result.Provider,
		RequestedModel: result.RequestedModel,
		TokensPrompt:   result.Tokens.Prompt,
		TokensTotal:    result.Tokens.Total,
		CostUSD:        result.CostUSD,
		ToolCalls:      make([]dryRunToolCall, 0, len(result.ToolCalls)),
	}
	for _, rec := range result.ToolCalls {
		args, argsTrunc := truncateField(rec.Arguments)
		res, resTrunc := truncateField(rec.Result)
		call := dryRunToolCall{
			Tool:       rec.ToolName,
			Server:     rec.ServerName,
			Round:      rec.Round,
			Outcome:    rec.Outcome,
			Suppressed: rec.Outcome == "suppressed",
			DurationMs: rec.DurationMs,
			Arguments:  args,
			Result:     res,
			Truncated:  argsTrunc || resTrunc,
			Error:      rec.ErrorMsg,
		}
		if call.Suppressed {
			t.SuppressedN++
		}
		t.ToolCalls = append(t.ToolCalls, call)
	}
	return t
}

// evalAuditMode returns the configured dry-run/eval audit mode.
func (s *Server) evalAuditMode() string {
	if s.deps.Config == nil {
		return agent.AuditFull
	}
	return s.deps.Config.Eval.AuditMode()
}

// emitDryRunAudit records that a preview happened. This is the coarse anchor
// on top of the per-round events: it survives the "summary" opt-down, and it
// is the one event that says *who* asked for the preview and on what.
func (s *Server) emitDryRunAudit(r *http.Request, agentName, target, convID string, asOf time.Time, result *agent.TurnResult, runErr error) {
	if s.deps.Auditor == nil {
		return
	}
	detail := map[string]any{
		"target": target,
		"as_of":  asOf.Format(time.RFC3339),
	}
	if result != nil && result.RequestedModel != "" {
		detail["model_override"] = result.RequestedModel
	}
	status := audit.StatusOK
	summary := fmt.Sprintf("Dry run: %s", target)
	if runErr != nil {
		status = audit.StatusError
		detail["error"] = runErr.Error()
		summary += " (failed)"
	} else if result != nil {
		detail["rounds"] = result.Rounds
		detail["cost"] = result.CostUSD
		detail["tool_calls"] = len(result.ToolCalls)
	}
	body, _ := json.Marshal(detail)
	s.deps.Auditor.Emit(r.Context(), audit.Event{
		Category:       audit.CategoryEval,
		Action:         "dry_run",
		Agent:          agentName + "#" + string(agent.ExecDryRun),
		Summary:        summary,
		Detail:         string(body),
		Status:         status,
		Source:         string(agent.ExecDryRun),
		ConversationID: convID,
	})
}

// runDryRun executes the turn and writes the transcript or the error.
func (s *Server) runDryRun(w http.ResponseWriter, r *http.Request, e *agent.Engine, msg adapter.IncomingMessage, policy agent.ExecPolicy, target string) {
	result, err := e.DryRun(r.Context(), msg, policy)
	s.emitDryRunAudit(r, e.Name(), target, policy.ConvID, policy.AsOf, result, err)
	if err != nil {
		s.logger.Warn("dry run failed", "agent", e.Name(), "target", target, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("dry run failed: %v", err)})
		return
	}
	s.logger.Info("dry run complete", "agent", e.Name(), "target", target,
		"rounds", result.Rounds, "suppressed", len(result.ToolCalls), "cost", result.CostUSD)
	writeJSON(w, http.StatusOK, buildTranscript(e.Name(), result))
}

// handleDryRunSchedule godoc
// @Summary Preview what a schedule would do
// @Description Runs the schedule's message through its agent under a dry-run execution policy: the real persona, skills and read-only tools apply, but every write is suppressed and nothing is persisted, sent to an adapter, or remembered. Returns the full transcript with suppressed calls marked. Costs real tokens.
// @Tags schedules
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Schedule name"
// @Param body body dryRunInput false "Optional as_of (RFC3339) to pin the clock and model to override the agent's model"
// @Success 200 {object} dryRunTranscript "Dry-run transcript"
// @Failure 400 {object} map[string]string "Invalid JSON or as_of"
// @Failure 404 {object} map[string]string "Schedule or agent not found"
// @Failure 500 {object} map[string]string "Dry run failed"
// @Failure 503 {object} map[string]string "Schedule management not available"
// @Router /schedules/{name}/dry-run [post]
func (s *Server) handleDryRunSchedule(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scheduler == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "schedule management is not available",
		})
		return
	}

	name := r.PathValue("name")
	entry, ok := s.deps.Scheduler.GetEntry(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("schedule %q not found", name)})
		return
	}

	input, asOf, err := parseDryRunInput(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	agentName := entry.Agent
	if agentName == "" {
		if fb := s.deps.Dispatcher.FallbackAgent(); fb != nil {
			agentName = fb.Name()
		}
	}
	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	// Build the message through the same helper the live job uses, so the
	// preview is the message that fires — header, skill and cron included.
	// The adapter binding is deliberately left empty: a dry run has no
	// delivery target, and the engine never reaches an adapter under a policy.
	cfg := scheduler.Config{
		Name:        entry.Name,
		Type:        string(entry.Type),
		Schedule:    entry.Expr,
		Skill:       entry.Skill,
		Agent:       agentName,
		SessionTier: entry.SessionTier,
		SessionMode: entry.SessionMode,
		Channel:     entry.Channel,
	}
	convID := newDryRunConvID()
	msg := configmcp.BuildScheduledMessage(cfg, agent.AdapterBinding{}, convID, asOf, e.Location())

	s.runDryRun(w, r, e, msg, agent.ExecPolicy{
		Kind:      agent.ExecDryRun,
		ConvID:    convID,
		AsOf:      asOf,
		Model:     input.Model,
		AuditMode: s.evalAuditMode(),
	}, "schedule "+name)
}

// handleDryRunSkill godoc
// @Summary Preview what a skill would do for a given message
// @Description Runs a message through the agent under a dry-run execution policy, naming the skill so its body is injected exactly as a scheduled invocation would. Real persona and read-only tools apply; every write is suppressed and nothing is persisted, sent, or remembered. Costs real tokens.
// @Tags skills
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param name path string true "Skill name"
// @Param body body dryRunInput true "Message to run, plus optional as_of (RFC3339) and model override"
// @Success 200 {object} dryRunTranscript "Dry-run transcript"
// @Failure 400 {object} map[string]string "Invalid JSON, missing message, or invalid as_of"
// @Failure 404 {object} map[string]string "Agent or skill not found"
// @Failure 500 {object} map[string]string "Dry run failed"
// @Router /skills/{agent}/{name}/dry-run [post]
func (s *Server) handleDryRunSkill(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("agent")
	skillName := r.PathValue("name")

	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}
	if _, ok := e.GetSkill(skillName); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("skill %q not found", skillName)})
		return
	}

	input, asOf, err := parseDryRunInput(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(input.Message) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}

	convID := newDryRunConvID()
	msg := adapter.IncomingMessage{
		ConversationID: convID,
		UserName:       "dry-run",
		Text:           input.Message,
		// Naming the skill injects its body the way a scheduled run does, so
		// the preview tests the skill rather than hoping a trigger matches.
		SkillName: skillName,
		Timestamp: asOf,
	}

	s.runDryRun(w, r, e, msg, agent.ExecPolicy{
		Kind:      agent.ExecDryRun,
		ConvID:    convID,
		AsOf:      asOf,
		Model:     input.Model,
		AuditMode: s.evalAuditMode(),
	}, "skill "+agentName+"/"+skillName)
}
