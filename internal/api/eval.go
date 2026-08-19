package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/eval"
)

// --- Request/response shapes ---

// evalTaskSetInput creates or renames a task set.
type evalTaskSetInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// evalTaskInput is the body of a task create or patch. Absent fields are left
// unchanged on a patch; on a create they take their defaults.
type evalTaskInput struct {
	Prompt   *string `json:"prompt"`
	Category *string `json:"category"`
	// PinnedHistory is a JSON array of {role, content} replayed verbatim as the
	// context preceding the turn. Captured at save time on purpose: the source
	// conversation drifts, and its latest window is not the window that
	// preceded the saved message.
	PinnedHistory        json.RawMessage `json:"pinned_history"`
	Tags                 json.RawMessage `json:"tags"`
	Notes                *string         `json:"notes"`
	SourceConversationID *string         `json:"source_conversation_id"`
	SourceMessageID      *int64          `json:"source_message_id"`
}

// evalVariantInput is one side of a comparison.
type evalVariantInput struct {
	Name        string `json:"name"`
	LLMModel    string `json:"llm_model"`
	LLMProvider string `json:"llm_provider"`
}

// evalRunInput creates a run.
type evalRunInput struct {
	TaskSet   string             `json:"task_set"`
	BaseAgent string             `json:"base_agent"`
	Variants  []evalVariantInput `json:"variants"`
	// K is samples per (task, variant). Omitted uses [eval] default_k.
	K int `json:"k"`
	// CostCap is the run's USD ceiling. Omitted uses [eval] max_cost_per_run.
	CostCap float64 `json:"cost_cap"`
	// AsOf pins the clock (RFC3339) so a replay is date-deterministic.
	AsOf string `json:"as_of"`
	// SampleTasks runs a stratified random subset of the set instead of all of
	// it. 0 or a value at or above the set size runs everything. The server
	// draws, because the drawn ids are recorded on the run and every expected-
	// sample figure counts what was drawn.
	SampleTasks int `json:"sample_tasks"`
}

// evalTaskSetDetail is a task set with its tasks.
type evalTaskSetDetail struct {
	eval.TaskSet
	Tasks []eval.Task `json:"tasks"`
}

// evalRunDetail is a run's live status: progress, spend and a rough ETA.
type evalRunDetail struct {
	eval.Run
	Variants     []eval.Variant `json:"variants"`
	SamplesDone  int            `json:"samples_done"`
	SamplesTotal int            `json:"samples_total"`
	ETASeconds   int            `json:"eta_seconds,omitempty"`
	Active       bool           `json:"active"`
}

// evalRunCreated is the create response.
type evalRunCreated struct {
	eval.Run
	Variants []eval.Variant `json:"variants"`
}

// evalImportResult reports how many tasks an import appended.
type evalImportResult struct {
	Imported int `json:"imported"`
}

// --- Guards and helpers ---

// evalRequired writes a 503 when the eval subsystem is not wired.
func (s *Server) evalRequired(w http.ResponseWriter) bool {
	if s.deps.EvalStore == nil || s.deps.EvalRunner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "eval subsystem not configured"})
		return false
	}
	return true
}

// writeEvalError maps the package's sentinels onto status codes: ErrNotFound
// is a 404, the two conflict sentinels are 409, everything else is a 500.
func writeEvalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, eval.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, eval.ErrNameTaken), errors.Is(err, eval.ErrTaskSetInUse):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// decodeEvalBody decodes a JSON body, writing a 400 and reporting false on
// failure. An empty body is accepted as an empty struct.
func decodeEvalBody(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil && err.Error() != "EOF" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return false
	}
	return true
}

// evalRunID parses the {id} path value.
func evalRunID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id must be a positive integer"})
		return 0, false
	}
	return id, true
}

// evalSummaryOpts is the [eval] policy a summary is computed against. A nil
// config leaves every field zero, and eval.SummaryOpts fills in the shipped
// defaults — a zero threshold would otherwise fail every gate.
func (s *Server) evalSummaryOpts() eval.SummaryOpts {
	if s.deps.Config == nil {
		return eval.SummaryOpts{}
	}
	c := s.deps.Config.Eval
	return eval.SummaryOpts{
		CompletenessFloor: c.CompletenessFloor,
		WinThreshold:      c.WinThreshold,
		GateRejectedPP:    c.GateRejectedRatePP,
		GateRoundsPct:     c.GateRoundsPct,
		GateCostPct:       c.GateCostPct,
	}
}

// --- Task sets ---

// handleCreateEvalTaskSet godoc
// @Summary Create an eval task set
// @Description Creates a named, empty set of eval test cases. Names are unique; tasks are added separately or imported as JSONL.
// @Tags eval
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body evalTaskSetInput true "Set name and optional description"
// @Success 201 {object} eval.TaskSet "Created task set"
// @Failure 400 {object} map[string]string "Invalid JSON or missing name"
// @Failure 409 {object} map[string]string "A set with that name already exists"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets [post]
func (s *Server) handleCreateEvalTaskSet(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	var input evalTaskSetInput
	if !decodeEvalBody(w, r, &input) {
		return
	}
	if input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	desc := ""
	if input.Description != nil {
		desc = *input.Description
	}
	set, err := s.deps.EvalStore.CreateTaskSet(r.Context(), strings.TrimSpace(*input.Name), desc)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, set)
}

// handleListEvalTaskSets godoc
// @Summary List eval task sets
// @Description Returns every task set with the number of tasks it holds.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Success 200 {array} eval.TaskSet "Task sets"
// @Failure 500 {object} map[string]string "Store error"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets [get]
func (s *Server) handleListEvalTaskSets(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	sets, err := s.deps.EvalStore.ListTaskSets(r.Context())
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sets)
}

// handleGetEvalTaskSet godoc
// @Summary Get an eval task set with its tasks
// @Description Returns one task set and every task in it, in creation order.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param name path string true "Task set name"
// @Success 200 {object} evalTaskSetDetail "Task set with tasks"
// @Failure 404 {object} map[string]string "Task set not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets/{name} [get]
func (s *Server) handleGetEvalTaskSet(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	set, err := s.deps.EvalStore.GetTaskSet(r.Context(), r.PathValue("name"))
	if err != nil {
		writeEvalError(w, err)
		return
	}
	tasks, err := s.deps.EvalStore.ListTasks(r.Context(), set.ID)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evalTaskSetDetail{TaskSet: *set, Tasks: tasks})
}

// handleUpdateEvalTaskSet godoc
// @Summary Rename an eval task set or change its description
// @Description Applies a partial update. Omitted fields are left unchanged.
// @Tags eval
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Task set name"
// @Param body body evalTaskSetInput true "Fields to change"
// @Success 200 {object} eval.TaskSet "Updated task set"
// @Failure 400 {object} map[string]string "Invalid JSON or blank name"
// @Failure 404 {object} map[string]string "Task set not found"
// @Failure 409 {object} map[string]string "The new name is taken"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets/{name} [patch]
func (s *Server) handleUpdateEvalTaskSet(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	var input evalTaskSetInput
	if !decodeEvalBody(w, r, &input) {
		return
	}
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name cannot be blank"})
		return
	}
	set, err := s.deps.EvalStore.UpdateTaskSet(r.Context(), r.PathValue("name"), input.Name, input.Description)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

// handleDeleteEvalTaskSet godoc
// @Summary Delete an eval task set
// @Description Removes a set and its tasks. Refused with 409 while any run references the set: a run's samples are only interpretable against the tasks that produced them, so the delete would either orphan results or silently cascade them away.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param name path string true "Task set name"
// @Success 204 "Deleted"
// @Failure 404 {object} map[string]string "Task set not found"
// @Failure 409 {object} map[string]string "Runs reference this task set"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets/{name} [delete]
func (s *Server) handleDeleteEvalTaskSet(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	if err := s.deps.EvalStore.DeleteTaskSet(r.Context(), r.PathValue("name")); err != nil {
		writeEvalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Tasks ---

// handleCreateEvalTask godoc
// @Summary Add a task to an eval task set
// @Description Appends one test case. This is what the Chat "Save as test case" action calls. pinned_history is captured now and replayed verbatim at run time, rather than re-read from the source conversation, which drifts.
// @Tags eval
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Task set name"
// @Param body body evalTaskInput true "Task fields; prompt is required, category defaults to chat"
// @Success 201 {object} eval.Task "Created task"
// @Failure 400 {object} map[string]string "Invalid JSON, missing prompt, or unknown category"
// @Failure 404 {object} map[string]string "Task set not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets/{name}/tasks [post]
func (s *Server) handleCreateEvalTask(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	set, err := s.deps.EvalStore.GetTaskSet(r.Context(), r.PathValue("name"))
	if err != nil {
		writeEvalError(w, err)
		return
	}
	var input evalTaskInput
	if !decodeEvalBody(w, r, &input) {
		return
	}
	if input.Prompt == nil || strings.TrimSpace(*input.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt is required"})
		return
	}
	task := eval.Task{
		Prompt:   *input.Prompt,
		Category: eval.CategoryChat,
		Tags:     "[]",
	}
	if input.Category != nil {
		task.Category = *input.Category
	}
	if !eval.ValidCategory(task.Category) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("invalid category %q: want one of %s",
				task.Category, strings.Join(eval.Categories(), ", "))})
		return
	}
	if len(input.PinnedHistory) > 0 {
		task.PinnedHistory = string(input.PinnedHistory)
	}
	if len(input.Tags) > 0 {
		task.Tags = string(input.Tags)
	}
	if input.Notes != nil {
		task.Notes = *input.Notes
	}
	if input.SourceConversationID != nil {
		task.SourceConversationID = *input.SourceConversationID
	}
	task.SourceMessageID = input.SourceMessageID

	created, err := s.deps.EvalStore.AddTask(r.Context(), set.ID, task)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleUpdateEvalTask godoc
// @Summary Edit a task in an eval task set
// @Description Applies a partial update. Omitted fields are left unchanged.
// @Tags eval
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Task set name"
// @Param id path int true "Task id"
// @Param body body evalTaskInput true "Fields to change"
// @Success 200 {object} eval.Task "Updated task"
// @Failure 400 {object} map[string]string "Invalid JSON, bad id, or unknown category"
// @Failure 404 {object} map[string]string "Task set or task not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets/{name}/tasks/{id} [patch]
func (s *Server) handleUpdateEvalTask(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	set, taskID, ok := s.resolveEvalTask(w, r)
	if !ok {
		return
	}
	var input evalTaskInput
	if !decodeEvalBody(w, r, &input) {
		return
	}
	if input.Category != nil && !eval.ValidCategory(*input.Category) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("invalid category %q: want one of %s",
				*input.Category, strings.Join(eval.Categories(), ", "))})
		return
	}
	patch := eval.TaskPatch{
		Prompt:   input.Prompt,
		Category: input.Category,
		Notes:    input.Notes,
	}
	if len(input.PinnedHistory) > 0 {
		pinned := string(input.PinnedHistory)
		patch.PinnedHistory = &pinned
	}
	if len(input.Tags) > 0 {
		tags := string(input.Tags)
		patch.Tags = &tags
	}
	task, err := s.deps.EvalStore.UpdateTask(r.Context(), set.ID, taskID, patch)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// handleDeleteEvalTask godoc
// @Summary Remove a task from an eval task set
// @Description Deletes one test case. Samples already recorded against it are left alone.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param name path string true "Task set name"
// @Param id path int true "Task id"
// @Success 204 "Deleted"
// @Failure 400 {object} map[string]string "Bad task id"
// @Failure 404 {object} map[string]string "Task set or task not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets/{name}/tasks/{id} [delete]
func (s *Server) handleDeleteEvalTask(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	set, taskID, ok := s.resolveEvalTask(w, r)
	if !ok {
		return
	}
	if err := s.deps.EvalStore.DeleteTask(r.Context(), set.ID, taskID); err != nil {
		writeEvalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveEvalTask resolves the {name}/{id} pair, writing the error response
// itself. Tasks are addressed through their set so one set's route cannot
// reach another's task.
func (s *Server) resolveEvalTask(w http.ResponseWriter, r *http.Request) (*eval.TaskSet, int64, bool) {
	set, err := s.deps.EvalStore.GetTaskSet(r.Context(), r.PathValue("name"))
	if err != nil {
		writeEvalError(w, err)
		return nil, 0, false
	}
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || taskID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task id must be a positive integer"})
		return nil, 0, false
	}
	return set, taskID, true
}

// --- JSONL import/export ---

// handleExportEvalTaskSet godoc
// @Summary Export an eval task set as JSONL
// @Description Streams one task per line, for hand-editing, git-versioning a curated set, or moving it between instances. Source ids are written for provenance and ignored on import.
// @Tags eval
// @Produce plain
// @Security BearerAuth
// @Param name path string true "Task set name"
// @Success 200 {string} string "JSONL, one task per line"
// @Failure 404 {object} map[string]string "Task set not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets/{name}/export [get]
func (s *Server) handleExportEvalTaskSet(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	name := r.PathValue("name")
	set, err := s.deps.EvalStore.GetTaskSet(r.Context(), name)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/jsonl")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".jsonl"))
	if err := s.deps.EvalStore.ExportJSONL(r.Context(), set.ID, w); err != nil {
		// The status line is already out, so the only honest signal left is
		// the log plus a truncated body.
		s.logger.Error("eval task set export failed", "set", name, "error", err)
	}
}

// handleImportEvalTaskSet godoc
// @Summary Import JSONL tasks into an eval task set
// @Description Appends every line of the body to the set. All-or-none: every line is parsed and validated first, so a typo halfway down a hand-edited file leaves the set exactly as it was rather than half-imported. The 400 names the offending line.
// @Tags eval
// @Accept plain
// @Produce json
// @Security BearerAuth
// @Param name path string true "Task set name"
// @Param body body string true "JSONL, one task object per line"
// @Success 200 {object} evalImportResult "Number of tasks appended"
// @Failure 400 {object} map[string]string "A line failed validation; nothing was imported"
// @Failure 404 {object} map[string]string "Task set not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/task-sets/{name}/import [post]
func (s *Server) handleImportEvalTaskSet(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	set, err := s.deps.EvalStore.GetTaskSet(r.Context(), r.PathValue("name"))
	if err != nil {
		writeEvalError(w, err)
		return
	}
	n, err := s.deps.EvalStore.ImportJSONL(r.Context(), set.ID, r.Body)
	if err != nil {
		var impErr *eval.ImportError
		if errors.As(err, &impErr) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": impErr.Error()})
			return
		}
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evalImportResult{Imported: n})
}

// --- Runs ---

// handleCreateEvalRun godoc
// @Summary Start an eval comparison run
// @Description Creates and launches a background run: every task in the set is executed k times against each variant, on the base agent's live engine under the eval execution policy. Reads run for real, writes are suppressed, and nothing is persisted to conversations, telemetry or memory. Costs real tokens, bounded by cost_cap and by [eval] max_concurrent. By convention the first variant is the incumbent (empty overlay = live config) and per-task deltas are measured against it. Pass sample_tasks to run a stratified random subset instead of the whole set; the drawn task ids are pinned on the run, so a task added to the set later cannot retroactively change what the run was measured over.
// @Tags eval
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body evalRunInput true "Task set, base agent, and two or more variants"
// @Success 201 {object} evalRunCreated "Created run with its variants"
// @Failure 400 {object} map[string]string "Invalid JSON, fewer than two variants, unknown agent/provider/model, or an empty task set"
// @Failure 404 {object} map[string]string "Task set not found"
// @Failure 500 {object} map[string]string "Store error"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/runs [post]
func (s *Server) handleCreateEvalRun(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	var input evalRunInput
	if !decodeEvalBody(w, r, &input) {
		return
	}

	set, err := s.deps.EvalStore.GetTaskSet(r.Context(), input.TaskSet)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	if set.TaskCount == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("task set %q has no tasks", input.TaskSet)})
		return
	}

	e := s.deps.Dispatcher.Agent(input.BaseAgent)
	if e == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("agent %q not found", input.BaseAgent)})
		return
	}

	variants, errMsg := s.buildEvalVariants(r, e, input.Variants)
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	draft, errMsg, err := s.draftEvalRun(r, set, input)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	run, created, err := s.deps.EvalStore.CreateRun(r.Context(), draft, variants)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	if err := s.deps.EvalRunner.StartRun(r.Context(), run.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("starting run: %v", err)})
		return
	}
	s.logger.Info("eval run started", "run", run.ID, "agent", input.BaseAgent,
		"task_set", input.TaskSet, "k", run.K, "cost_cap", run.CostCap,
		"variants", len(created), "tasks", run.TaskCount, "sampled", len(run.TaskIDs) > 0)
	writeJSON(w, http.StatusCreated, evalRunCreated{Run: *run, Variants: created})
}

// draftEvalRun resolves the run's own parameters — clock, k, cap and the
// pinned task list — returning a client-error message instead on rejection.
//
// The subset is drawn here rather than by the caller because the drawn ids are
// what the run is measured over: they go on the row, and every expected-sample
// figure counts them.
func (s *Server) draftEvalRun(r *http.Request, set *eval.TaskSet, input evalRunInput) (eval.Run, string, error) {
	asOf := time.Now()
	if input.AsOf != "" {
		parsed, err := time.Parse(time.RFC3339, input.AsOf)
		if err != nil {
			return eval.Run{}, "invalid as_of: must be RFC3339", nil
		}
		asOf = parsed
	}

	cfg := s.deps.EvalRunner.Config()
	k := input.K
	if k <= 0 {
		k = cfg.DefaultK
	}
	costCap := input.CostCap
	if costCap <= 0 {
		costCap = cfg.MaxCostPerRun
	}

	var taskIDs eval.TaskIDList
	if input.SampleTasks < 0 {
		return eval.Run{}, "sample_tasks cannot be negative", nil
	}
	if input.SampleTasks > 0 {
		tasks, err := s.deps.EvalStore.ListTasks(r.Context(), set.ID)
		if err != nil {
			return eval.Run{}, "", err
		}
		taskIDs = eval.DrawStratified(tasks, input.SampleTasks)
	}

	return eval.Run{
		TaskSetID: set.ID,
		BaseAgent: input.BaseAgent,
		K:         k,
		CostCap:   costCap,
		AsOf:      asOf,
		TaskIDs:   taskIDs,
	}, "", nil
}

// buildEvalVariants validates the requested variants and encodes their
// overlays. Returns a client-error message instead on rejection.
//
// A provider is checked against the router up front rather than at request
// time: an unregistered name would otherwise fail every sample in the run
// instead of the one call that created it. Models are validated fail-open, the
// same way dry-run overrides are — a provider's advertised list moves, so only
// a name it positively does not know is rejected.
func (s *Server) buildEvalVariants(r *http.Request, e *agent.Engine, inputs []evalVariantInput) ([]eval.Variant, string) {
	if len(inputs) < 2 {
		return nil, "a run needs at least 2 variants to compare"
	}
	router := e.LLMRouter()
	seen := make(map[string]struct{}, len(inputs))
	out := make([]eval.Variant, 0, len(inputs))

	for _, in := range inputs {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, "every variant needs a name"
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Sprintf("duplicate variant name %q", name)
		}
		seen[name] = struct{}{}

		if in.LLMProvider != "" && router != nil && !router.HasProvider(in.LLMProvider) {
			return nil, fmt.Sprintf("variant %q: provider %q is not registered", name, in.LLMProvider)
		}
		if errMsg := s.validateModelOverride(r.Context(), e, in.LLMModel); errMsg != "" {
			return nil, fmt.Sprintf("variant %q: %s", name, errMsg)
		}

		overlay, err := json.Marshal(eval.Overlay{Model: in.LLMModel, Provider: in.LLMProvider})
		if err != nil {
			return nil, fmt.Sprintf("variant %q: encoding overlay: %v", name, err)
		}
		out = append(out, eval.Variant{Name: name, Overlay: string(overlay)})
	}
	return out, ""
}

// handleListEvalRuns godoc
// @Summary List eval runs
// @Description Returns runs newest first, optionally filtered by task set name and status.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param task_set query string false "Filter by task set name"
// @Param status query string false "Filter by status (pending, running, done, capped, stopped, failed)"
// @Success 200 {array} eval.Run "Runs"
// @Failure 404 {object} map[string]string "Named task set not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/runs [get]
func (s *Server) handleListEvalRuns(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	var setID int64
	if name := r.URL.Query().Get("task_set"); name != "" {
		set, err := s.deps.EvalStore.GetTaskSet(r.Context(), name)
		if err != nil {
			writeEvalError(w, err)
			return
		}
		setID = set.ID
	}
	runs, err := s.deps.EvalStore.ListRuns(r.Context(), setID, r.URL.Query().Get("status"))
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleGetEvalRun godoc
// @Summary Get an eval run's status and progress
// @Description Returns the run with its variants, samples done out of expected, spend against the cap, and a rough ETA (mean sample latency times remaining, divided by concurrency). samples_total counts the run's pinned task list when it has one, so a task added to the set after the run was created does not inflate it. This is the authoritative view; the eval_progress WebSocket frame is a droppable convenience on top.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param id path int true "Run id"
// @Success 200 {object} evalRunDetail "Run status"
// @Failure 400 {object} map[string]string "Bad run id"
// @Failure 404 {object} map[string]string "Run not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/runs/{id} [get]
func (s *Server) handleGetEvalRun(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	id, ok := evalRunID(w, r)
	if !ok {
		return
	}
	run, err := s.deps.EvalStore.GetRun(r.Context(), id)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	variants, err := s.deps.EvalStore.ListVariants(r.Context(), id)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	tasks, err := s.deps.EvalStore.RunTasks(r.Context(), run)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	samples, err := s.deps.EvalStore.ListSamples(r.Context(), id)
	if err != nil {
		writeEvalError(w, err)
		return
	}

	detail := evalRunDetail{
		Run:          *run,
		Variants:     variants,
		SamplesDone:  len(samples),
		SamplesTotal: len(tasks) * len(variants) * run.K,
		Active:       s.deps.EvalRunner.IsActive(id),
	}
	detail.ETASeconds = evalETA(samples, detail.SamplesTotal, s.deps.EvalRunner.Config().MaxConcurrent)
	writeJSON(w, http.StatusOK, detail)
}

// evalETA estimates remaining seconds from the mean latency of the samples
// that have landed. Zero once nothing is left or nothing has landed yet.
func evalETA(samples []eval.Sample, total, concurrency int) int {
	var sum int64
	var n int
	for _, smp := range samples {
		if smp.Status == eval.SampleOK {
			sum += smp.LatencyMs
			n++
		}
	}
	remaining := total - len(samples)
	if n == 0 || remaining <= 0 {
		return 0
	}
	if concurrency < 1 {
		concurrency = 1
	}
	meanMs := float64(sum) / float64(n)
	return int(meanMs * float64(remaining) / float64(concurrency) / 1000)
}

// handleStopEvalRun godoc
// @Summary Stop an eval run
// @Description Cancels an active run. In-flight LLM calls die on the context, queued samples never start, and the run finishes 'stopped' with the samples it already produced. A run that has already reached a terminal status returns 409.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param id path int true "Run id"
// @Success 200 {object} map[string]string "Stop requested"
// @Failure 400 {object} map[string]string "Bad run id"
// @Failure 404 {object} map[string]string "Run not found"
// @Failure 409 {object} map[string]string "Run is not active"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/runs/{id}/stop [post]
func (s *Server) handleStopEvalRun(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	id, ok := evalRunID(w, r)
	if !ok {
		return
	}
	run, err := s.deps.EvalStore.GetRun(r.Context(), id)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	if !s.deps.EvalRunner.Stop(id) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("run %d is %s and cannot be stopped", id, run.Status)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

// handleEvalRunSummary godoc
// @Summary Get an eval run's scorecard and verdict
// @Description Aggregates the run's samples: per-variant rejected and failed rates (tool-call level, with cached and suppressed calls excluded because nothing executed), mean rounds, wrap-up count, mean cost per task and latency, plus per-task deltas against the baseline variant and a completeness indicator. Each non-baseline variant also gets a verdict with its work shown: the objective gate table (value, delta, threshold, pass/fail), a one-line plain-language reason, the blinded-pair judge tally, the operator-judge agreement figure, and a per-category breakdown. The rule is asymmetric — the gates alone can declare a downgrade or report no regressions, but only the judge win-rate can declare an upgrade. A run below the [eval] completeness_floor reports its numbers but is flagged inconclusive rather than dressed up as a verdict.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param id path int true "Run id"
// @Success 200 {object} eval.Summary "Objective scorecard"
// @Failure 400 {object} map[string]string "Bad run id"
// @Failure 404 {object} map[string]string "Run not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/runs/{id}/summary [get]
func (s *Server) handleEvalRunSummary(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	id, ok := evalRunID(w, r)
	if !ok {
		return
	}
	summary, err := s.deps.EvalStore.Summarize(r.Context(), id, s.evalSummaryOpts())
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// handleEvalRunSamples godoc
// @Summary Get an eval run's per-sample transcripts
// @Description Returns every sample the run produced, including its response and the full tool trace with arguments and results (trimmed to 8 KiB per field). Failed samples carry their error instead.
// @Tags eval
// @Produce json
// @Security BearerAuth
// @Param id path int true "Run id"
// @Success 200 {array} eval.Sample "Samples"
// @Failure 400 {object} map[string]string "Bad run id"
// @Failure 404 {object} map[string]string "Run not found"
// @Failure 503 {object} map[string]string "Eval subsystem not configured"
// @Router /eval/runs/{id}/samples [get]
func (s *Server) handleEvalRunSamples(w http.ResponseWriter, r *http.Request) {
	if !s.evalRequired(w) {
		return
	}
	id, ok := evalRunID(w, r)
	if !ok {
		return
	}
	if _, err := s.deps.EvalStore.GetRun(r.Context(), id); err != nil {
		writeEvalError(w, err)
		return
	}
	samples, err := s.deps.EvalStore.ListSamples(r.Context(), id)
	if err != nil {
		writeEvalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, samples)
}
