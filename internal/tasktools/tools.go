// Package tasktools registers the task MCP tools. The per-agent config MCP and
// the external MCP server both call Register, so den agents and outside
// harnesses (Claude Code) see the same tool names, inputs and results. Only
// who the caller is differs, and the Authorizer supplies that.
package tasktools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/task"
)

// Authorizer returns the member ID a call acts as, or a tool result that
// refuses the call. write is true for tools that change tasks.
type Authorizer func(ctx context.Context, write bool) (actor string, denied *mcp.CallToolResult)

// openStatuses is what task_list returns when no status is given.
var openStatuses = []string{task.StatusTodo, task.StatusInProgress, task.StatusReview, task.StatusBlocked}

// closedWindow is how far back include_closed reaches.
const closedWindow = 7 * 24 * time.Hour

const protocol = "Tasks are how work is handed between den agents, outside tools and the operator. " +
	"Claim a task with task_checkout before working on it. If a claim is refused, don't retry: pick another task or comment. "

type listInput struct {
	Assignee      string   `json:"assignee,omitempty" jsonschema:"Whose tasks: me (default), any, none (unassigned), or a member name"`
	Status        []string `json:"status,omitempty" jsonschema:"Statuses to include. Default: todo, in_progress, review, blocked"`
	Parent        string   `json:"parent,omitempty" jsonschema:"Only subtasks of this task, e.g. DK-131"`
	IncludeClosed bool     `json:"include_closed,omitempty" jsonschema:"Also return tasks closed in the last 7 days"`
	Members       bool     `json:"members,omitempty" jsonschema:"Also return everyone a task can be assigned to"`
}

type idInput struct {
	ID string `json:"id" jsonschema:"Task key or number, e.g. DK-131"`
}

type createInput struct {
	Title    string `json:"title" jsonschema:"Short title"`
	Body     string `json:"body,omitempty" jsonschema:"What needs doing and what done looks like"`
	Assignee string `json:"assignee,omitempty" jsonschema:"Member name, me, or empty to leave it unassigned for anyone to claim"`
	Parent   string `json:"parent,omitempty" jsonschema:"Make it a subtask of this task, e.g. DK-131"`
	NoReview bool   `json:"no_review,omitempty" jsonschema:"Skip review. By default you, the filer, review it before it's done"`
}

type commentInput struct {
	ID   string `json:"id" jsonschema:"Task key or number, e.g. DK-131"`
	Body string `json:"body" jsonschema:"The comment"`
}

type updateInput struct {
	ID       string  `json:"id" jsonschema:"Task key or number, e.g. DK-131"`
	Status   string  `json:"status,omitempty" jsonschema:"New status: todo, review, blocked, done or cancelled. Use task_checkout to start work"`
	Reason   string  `json:"reason,omitempty" jsonschema:"Why it's blocked. Required for status blocked"`
	Assignee *string `json:"assignee,omitempty" jsonschema:"Hand the task to this member. Empty string unassigns"`
	Title    *string `json:"title,omitempty" jsonschema:"New title (filer only)"`
	Body     *string `json:"body,omitempty" jsonschema:"New body (filer only)"`
}

// Register adds the six task tools to srv.
func Register(srv *mcp.Server, svc *task.Service, auth Authorizer) {
	h := &handlers{svc: svc, auth: auth}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "task_list",
		Description: protocol + "Lists tasks, newest first. Defaults to your open tasks; pass assignee none to find unassigned work anyone may claim.",
	}, h.list)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "task_get",
		Description: "Returns one task with its full thread (comments and events in order), subtasks and parent.",
	}, h.get)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "task_create",
		Description: protocol + "Files a task. Assigning it is how you delegate: there is no separate delegate tool. Pass parent to split your own task into subtasks. Subtasks nest at most 3 deep.",
	}, h.create)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "task_checkout",
		Description: "Claims a task so nobody else works on it. Works on tasks assigned to you or unassigned. A refused claim names who holds it: don't retry.",
	}, h.checkout)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "task_comment",
		Description: "Adds a comment to a task's thread. Comments are the record of the work: post your plan, findings and result here.",
	}, h.comment)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "task_update",
		Description: "Changes a task's status, assignee, title or body. When you finish, move it to review if it has a reviewer, otherwise to done. Blocked needs a reason.",
	}, h.update)
}

type handlers struct {
	svc  *task.Service
	auth Authorizer
}

func (h *handlers) list(ctx context.Context, _ *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, any, error) {
	actor, denied := h.auth(ctx, false)
	if denied != nil {
		return denied, nil, nil
	}
	f, err := h.filter(ctx, actor, in)
	if err != nil {
		return failure(err), nil, nil
	}
	ts, err := h.svc.List(ctx, f)
	if err != nil {
		return failure(err), nil, nil
	}
	out := map[string]any{"tasks": briefs(ts)}
	if in.Members {
		ms, err := h.svc.Members(ctx)
		if err != nil {
			return failure(err), nil, nil
		}
		out["members"] = ms
	}
	return result(out)
}

func (h *handlers) filter(ctx context.Context, actor string, in listInput) (task.Filter, error) {
	f := task.Filter{Statuses: in.Status, ClosedSince: time.Now()}
	if len(f.Statuses) == 0 {
		f.Statuses = openStatuses
	}
	if in.IncludeClosed {
		f.ClosedSince = time.Now().Add(-closedWindow)
		if len(in.Status) == 0 {
			f.Statuses = nil
		}
	}
	for _, st := range f.Statuses {
		if !task.ValidStatus(st) {
			return f, &task.RuleError{Message: fmt.Sprintf("unknown status %q", st)}
		}
	}
	switch strings.ToLower(strings.TrimSpace(in.Assignee)) {
	case "", "me":
		f.Assignee = actor
	case "any":
	case "none":
		f.Unassigned = true
	default:
		id, err := h.svc.Resolve(ctx, in.Assignee, actor)
		if err != nil {
			return f, err
		}
		f.Assignee = id
	}
	if in.Parent != "" {
		id, err := parseID(in.Parent)
		if err != nil {
			return f, err
		}
		f.ParentID = &id
	}
	return f, nil
}

func (h *handlers) get(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
	if _, denied := h.auth(ctx, false); denied != nil {
		return denied, nil, nil
	}
	id, err := parseID(in.ID)
	if err != nil {
		return failure(err), nil, nil
	}
	d, err := h.svc.Detail(ctx, id)
	if err != nil {
		return failure(err), nil, nil
	}
	return result(detailOf(d))
}

func (h *handlers) create(ctx context.Context, _ *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, any, error) {
	actor, denied := h.auth(ctx, true)
	if denied != nil {
		return denied, nil, nil
	}
	req := task.CreateRequest{Title: in.Title, Body: in.Body, Assignee: in.Assignee, NoReview: in.NoReview}
	if in.Parent != "" {
		id, err := parseID(in.Parent)
		if err != nil {
			return failure(err), nil, nil
		}
		req.ParentID = &id
	}
	t, err := h.svc.Create(ctx, actor, req)
	if err != nil {
		return failure(err), nil, nil
	}
	return result(map[string]any{"task": briefOf(t), "note": assignedNote(t, actor)})
}

func (h *handlers) checkout(ctx context.Context, _ *mcp.CallToolRequest, in idInput) (*mcp.CallToolResult, any, error) {
	actor, denied := h.auth(ctx, true)
	if denied != nil {
		return denied, nil, nil
	}
	id, err := parseID(in.ID)
	if err != nil {
		return failure(err), nil, nil
	}
	t, err := h.svc.Checkout(ctx, actor, id)
	if err != nil {
		return failure(err), nil, nil
	}
	note := fmt.Sprintf("You hold %s. Read the thread with task_get, post progress with task_comment, and finish with task_update.", t.Key())
	return result(map[string]any{"task": briefOf(t), "note": note})
}

func (h *handlers) comment(ctx context.Context, _ *mcp.CallToolRequest, in commentInput) (*mcp.CallToolResult, any, error) {
	actor, denied := h.auth(ctx, true)
	if denied != nil {
		return denied, nil, nil
	}
	id, err := parseID(in.ID)
	if err != nil {
		return failure(err), nil, nil
	}
	ev, err := h.svc.Comment(ctx, actor, id, in.Body)
	if err != nil {
		return failure(err), nil, nil
	}
	return result(map[string]any{"task": task.Key(id), "comment_id": ev.ID})
}

func (h *handlers) update(ctx context.Context, _ *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, any, error) {
	actor, denied := h.auth(ctx, true)
	if denied != nil {
		return denied, nil, nil
	}
	id, err := parseID(in.ID)
	if err != nil {
		return failure(err), nil, nil
	}
	req := task.UpdateRequest{Title: in.Title, Body: in.Body, Assignee: in.Assignee, Reason: in.Reason}
	if in.Status != "" {
		req.Status = &in.Status
	}
	t, err := h.svc.Update(ctx, actor, id, req)
	if err != nil {
		return failure(err), nil, nil
	}
	out := map[string]any{"task": briefOf(t)}
	if in.Assignee != nil {
		out["note"] = assignedNote(t, actor)
	} else if t.Status == task.StatusReview && t.Reviewer != "" {
		out["note"] = fmt.Sprintf("%s reviews %s and closes it.", task.MemberName(t.Reviewer), t.Key())
	}
	return result(out)
}

// assignedNote tells the caller what happens next for the new assignee, so a
// run never waits on work that won't start inside it.
func assignedNote(t *task.Task, actor string) string {
	switch {
	case t.Assignee == "":
		return "Unassigned: anyone may claim it with task_checkout."
	case t.Assignee == actor:
		return fmt.Sprintf("Assigned to you. Claim %s with task_checkout when you start.", t.Key())
	case t.Assignee == task.Human:
		return "It's on the operator's list."
	case strings.HasPrefix(t.Assignee, task.ExternalPrefix):
		return fmt.Sprintf("%s picks it up when it next checks its tasks. Don't wait for it in this run.", task.MemberName(t.Assignee))
	default:
		return fmt.Sprintf("%s sees it the next time it checks task_list. Don't wait for it in this run.", task.MemberName(t.Assignee))
	}
}

// parseID accepts "DK-131", "dk-131" or "131".
func parseID(s string) (int64, error) {
	raw := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s)), "DK-")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, &task.RuleError{Message: fmt.Sprintf("invalid task id %q: use a key like DK-131", s)}
	}
	return id, nil
}

// brief is a task as the tools return it: enough to decide what to do next,
// without the body or thread.
type brief struct {
	Key           string `json:"key"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Assignee      string `json:"assignee,omitempty"`
	Holder        string `json:"holder,omitempty"`
	Reviewer      string `json:"reviewer,omitempty"`
	Parent        string `json:"parent,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`
	Subtasks      string `json:"subtasks,omitempty"` // "done/total"
	UpdatedAt     string `json:"updated_at"`
}

func briefOf(t *task.Task) brief {
	b := brief{
		Key: t.Key(), Title: t.Title, Status: t.Status, Assignee: t.Assignee, Holder: t.Holder,
		Reviewer: t.Reviewer, BlockedReason: t.BlockedReason, UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.ParentID != nil {
		b.Parent = task.Key(*t.ParentID)
	}
	if t.ChildrenTotal > 0 {
		b.Subtasks = fmt.Sprintf("%d/%d", t.ChildrenDone, t.ChildrenTotal)
	}
	return b
}

func briefs(ts []task.Task) []brief {
	out := make([]brief, len(ts))
	for i := range ts {
		out[i] = briefOf(&ts[i])
	}
	return out
}

type threadEntry struct {
	At    string `json:"at"`
	Actor string `json:"actor"`
	Kind  string `json:"kind"`
	Body  string `json:"body,omitempty"`
	Data  string `json:"data,omitempty"`
}

func detailOf(d *task.Detail) map[string]any {
	thread := make([]threadEntry, len(d.Events))
	for i, e := range d.Events {
		thread[i] = threadEntry{At: e.At.UTC().Format(time.RFC3339), Actor: e.Actor, Kind: e.Kind, Body: e.Body, Data: e.Data}
	}
	out := map[string]any{
		"task":     briefOf(&d.Task),
		"body":     d.Task.Body,
		"thread":   thread,
		"subtasks": briefs(d.Children),
	}
	if d.Parent != nil {
		out["parent"] = briefOf(d.Parent)
	}
	return out
}

func result(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding task tool result: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
}

// failure turns a task error into a tool error the model can read. Rule and
// conflict messages are already written for an agent; others get a prefix.
func failure(err error) *mcp.CallToolResult {
	msg := err.Error()
	var ce *task.ConflictError
	var re *task.RuleError
	if !errors.As(err, &ce) && !errors.As(err, &re) && !errors.Is(err, task.ErrNotFound) {
		msg = "task tool failed: " + msg
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}, IsError: true}
}
