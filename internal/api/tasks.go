package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/task"
)

// taskActor is who a REST call acts as. Only the operator — a dashboard
// session or an admin key — is task.Human. Any other key with tasks:write is
// the external member named after it, so a harness key can't use REST to act
// as the operator and skip review.
func (s *Server) taskActor(r *http.Request) string {
	name, isAdmin, _ := s.authenticate(r.Context(), r, "admin")
	if isAdmin {
		return task.Human
	}
	return task.ExternalID(name)
}

// defaultClosedWithin is how far back GET /tasks reaches for closed tasks.
const defaultClosedWithin = 7 * 24 * time.Hour

// taskView is a task as the API returns it: the row plus its display key.
type taskView struct {
	task.Task
	Key string `json:"key"`
}

// taskDetailView is a task with its thread, subtasks and parent.
type taskDetailView struct {
	Task     taskView     `json:"task"`
	Events   []task.Event `json:"events"`
	Children []taskView   `json:"children"`
	Parent   *taskView    `json:"parent,omitempty"`
}

type taskCreateInput struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Assignee string `json:"assignee"`  // member ID or name; empty = unassigned
	ParentID *int64 `json:"parent_id"` // makes the task a subtask
	NoReview bool   `json:"no_review"` // skip the "you review before it's done" step
}

type taskUpdateInput struct {
	Title    *string `json:"title"`
	Body     *string `json:"body"`
	Assignee *string `json:"assignee"` // "" unassigns
	Status   *string `json:"status"`
	Reason   string  `json:"reason"` // required when status is "blocked"
}

type taskCommentInput struct {
	Body string `json:"body"`
}

func viewOf(t *task.Task) taskView { return taskView{Task: *t, Key: t.Key()} }

func viewsOf(ts []task.Task) []taskView {
	out := make([]taskView, len(ts))
	for i := range ts {
		out[i] = viewOf(&ts[i])
	}
	return out
}

func (s *Server) tasksRequired(w http.ResponseWriter) bool {
	if s.deps.Tasks == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tasks are off: set [tasks] enabled = true in the config and restart"})
		return false
	}
	return true
}

// writeTaskError maps task errors to status codes: a lost claim is 409 and
// names the holder, a broken rule or bad input is 400, a missing task 404.
// Anything else is logged and answered with a generic 500, so storage errors
// never reach the client.
func (s *Server) writeTaskError(w http.ResponseWriter, err error) {
	var ce *task.ConflictError
	var re *task.RuleError
	switch {
	case errors.Is(err, task.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]string{"error": ce.Message, "holder": ce.Holder})
	case errors.As(err, &re):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": re.Message})
	default:
		s.logger.Error("task request failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func taskIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimPrefix(strings.ToUpper(r.PathValue("id")), "DK-")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid task id %q", r.PathValue("id"))})
		return 0, false
	}
	return id, true
}

// handleListTasks godoc
// @Summary List tasks
// @Description Returns tasks newest first: every open task, plus tasks closed within closed_within (default 168h). Each task carries its subtask counts.
// @Tags tasks
// @Produce json
// @Security BearerAuth
// @Param status query string false "Comma-separated statuses: todo, in_progress, review, blocked, done, cancelled"
// @Param assignee query string false "Member ID or name, e.g. agent:pamela, claude-code, human"
// @Param parent query int false "Only subtasks of this task"
// @Param top_level query bool false "Only tasks without a parent"
// @Param closed_within query string false "Go duration; how far back closed tasks reach (default 168h)"
// @Param limit query int false "Maximum tasks (default and cap 500)"
// @Success 200 {object} object "Object with a tasks array"
// @Failure 400 {object} map[string]string "Invalid filter"
// @Failure 503 {object} map[string]string "Tasks are off ([tasks] enabled = false)"
// @Router /tasks [get]
func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	if !s.tasksRequired(w) {
		return
	}
	f, err := s.taskFilter(r)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	ts, err := s.deps.Tasks.List(r.Context(), f)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": viewsOf(ts)})
}

func (s *Server) taskFilter(r *http.Request) (task.Filter, error) {
	q := r.URL.Query()
	within := defaultClosedWithin
	if v := q.Get("closed_within"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return task.Filter{}, &task.RuleError{Message: fmt.Sprintf("invalid closed_within %q", v)}
		}
		within = d
	}
	f := task.Filter{
		Statuses:    audit.ParseFilterList(q["status"]...),
		ClosedSince: time.Now().Add(-within),
		TopLevel:    q.Get("top_level") == "true",
	}
	for _, st := range f.Statuses {
		if !task.ValidStatus(st) {
			return task.Filter{}, &task.RuleError{Message: fmt.Sprintf("unknown status %q", st)}
		}
	}
	if v := q.Get("assignee"); v != "" {
		id, err := s.deps.Tasks.Resolve(r.Context(), v, s.taskActor(r))
		if err != nil {
			return task.Filter{}, err
		}
		f.Assignee = id
	}
	if v := q.Get("parent"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return task.Filter{}, &task.RuleError{Message: fmt.Sprintf("invalid parent %q", v)}
		}
		f.ParentID = &id
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return task.Filter{}, &task.RuleError{Message: fmt.Sprintf("invalid limit %q", v)}
		}
		f.Limit = n
	}
	return f, nil
}

// handleCreateTask godoc
// @Summary File a task
// @Description Files a task. Admin keys and dashboard sessions act as the operator (human); other keys act as the member named after the key. Assignee is optional: an unassigned task waits in To do and anyone may claim it. The operator reviews the task before it's done unless no_review is set.
// @Tags tasks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body taskCreateInput true "The new task"
// @Success 201 {object} taskView "The created task"
// @Failure 400 {object} map[string]string "Invalid input or a broken task rule"
// @Failure 404 {object} map[string]string "Parent task not found"
// @Failure 503 {object} map[string]string "Tasks are off ([tasks] enabled = false)"
// @Router /tasks [post]
func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksRequired(w) {
		return
	}
	var in taskCreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	t, err := s.deps.Tasks.Create(r.Context(), s.taskActor(r), task.CreateRequest{
		Title: in.Title, Body: in.Body, Assignee: in.Assignee, ParentID: in.ParentID, NoReview: in.NoReview,
	})
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewOf(t))
}

// handleTaskMembers godoc
// @Summary List task members
// @Description Returns everyone a task can be assigned to: the operator ("human"), each den agent ("agent:<name>") and each API key with tasks:write ("ext:<key name>").
// @Tags tasks
// @Produce json
// @Security BearerAuth
// @Success 200 {object} object "Object with a members array"
// @Failure 503 {object} map[string]string "Tasks are off ([tasks] enabled = false)"
// @Router /tasks/members [get]
func (s *Server) handleTaskMembers(w http.ResponseWriter, r *http.Request) {
	if !s.tasksRequired(w) {
		return
	}
	ms, err := s.deps.Tasks.Members(r.Context())
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": ms})
}

// handleGetTask godoc
// @Summary Get a task
// @Description Returns a task with its thread (comments and events in order), its subtasks and its parent. The id may be numeric or a display key like DK-131.
// @Tags tasks
// @Produce json
// @Security BearerAuth
// @Param id path string true "Task ID or key"
// @Success 200 {object} taskDetailView "The task with its thread"
// @Failure 400 {object} map[string]string "Invalid id"
// @Failure 404 {object} map[string]string "Task not found"
// @Failure 503 {object} map[string]string "Tasks are off ([tasks] enabled = false)"
// @Router /tasks/{id} [get]
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksRequired(w) {
		return
	}
	id, ok := taskIDParam(w, r)
	if !ok {
		return
	}
	d, err := s.deps.Tasks.Detail(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	out := taskDetailView{Task: viewOf(&d.Task), Events: d.Events, Children: viewsOf(d.Children)}
	if d.Parent != nil {
		p := viewOf(d.Parent)
		out.Parent = &p
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUpdateTask godoc
// @Summary Update a task
// @Description Changes a task's title, body, assignee or status. Admin keys and dashboard sessions act as the operator; other keys follow the member rules (e.g. only the reviewer closes a reviewed task). Fields left out are unchanged; assignee "" unassigns. Changes apply in the order edit, reassign, status. Reassigning drops any hold.
// @Tags tasks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Task ID or key"
// @Param body body taskUpdateInput true "Fields to change"
// @Success 200 {object} taskView "The updated task"
// @Failure 400 {object} map[string]string "Invalid input or a broken task rule"
// @Failure 404 {object} map[string]string "Task not found"
// @Failure 503 {object} map[string]string "Tasks are off ([tasks] enabled = false)"
// @Router /tasks/{id} [patch]
func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksRequired(w) {
		return
	}
	id, ok := taskIDParam(w, r)
	if !ok {
		return
	}
	var in taskUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	t, err := s.deps.Tasks.Update(r.Context(), s.taskActor(r), id, task.UpdateRequest{
		Title: in.Title, Body: in.Body, Assignee: in.Assignee, Status: in.Status, Reason: in.Reason,
	})
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewOf(t))
}

// handleCommentTask godoc
// @Summary Comment on a task
// @Description Adds a comment to a task's thread, authored by the caller (human for admin keys and sessions). Closed tasks take comments too.
// @Tags tasks
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Task ID or key"
// @Param body body taskCommentInput true "The comment"
// @Success 201 {object} task.Event "The comment event"
// @Failure 400 {object} map[string]string "Empty or oversized comment"
// @Failure 404 {object} map[string]string "Task not found"
// @Failure 503 {object} map[string]string "Tasks are off ([tasks] enabled = false)"
// @Router /tasks/{id}/comments [post]
func (s *Server) handleCommentTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksRequired(w) {
		return
	}
	id, ok := taskIDParam(w, r)
	if !ok {
		return
	}
	var in taskCommentInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	ev, err := s.deps.Tasks.Comment(r.Context(), s.taskActor(r), id, in.Body)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ev)
}

// handleReleaseTask godoc
// @Summary Release a task's hold
// @Description Drops whoever holds the task and returns in-progress work to To do. The assignee stays. The operator can release any claim; another key only its own.
// @Tags tasks
// @Produce json
// @Security BearerAuth
// @Param id path string true "Task ID or key"
// @Success 200 {object} taskView "The released task"
// @Failure 400 {object} map[string]string "The task isn't held"
// @Failure 404 {object} map[string]string "Task not found"
// @Failure 503 {object} map[string]string "Tasks are off ([tasks] enabled = false)"
// @Router /tasks/{id}/release [post]
func (s *Server) handleReleaseTask(w http.ResponseWriter, r *http.Request) {
	if !s.tasksRequired(w) {
		return
	}
	id, ok := taskIDParam(w, r)
	if !ok {
		return
	}
	t, err := s.deps.Tasks.Release(r.Context(), s.taskActor(r), id)
	if err != nil {
		s.writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewOf(t))
}
