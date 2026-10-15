//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/task"
)

type taskJSON struct {
	ID       int64  `json:"id"`
	Key      string `json:"key"`
	Status   string `json:"status"`
	Assignee string `json:"assignee"`
	Holder   string `json:"holder"`
	Reviewer string `json:"reviewer"`
}

func TestTasks_Lifecycle(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()

	// File an unassigned to-do, then hand it to the den agent.
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Add Fastmail to the weekly digest"}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created taskJSON
	DecodeJSON(t, rec, &created)
	if created.Assignee != "" || created.Reviewer != task.Human {
		t.Fatalf("created = %+v", created)
	}

	rec = h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/tasks/"+created.Key, map[string]any{"assignee": "default"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body.String())
	}

	// The agent side goes through the service, as the MCP tools will.
	agentID := task.AgentID("default")
	if _, err := h.Tasks.Checkout(ctx, agentID, created.ID); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := h.Tasks.Comment(ctx, agentID, created.ID, "Draft ready."); err != nil {
		t.Fatalf("comment: %v", err)
	}
	review := task.StatusReview
	if _, err := h.Tasks.Update(ctx, agentID, created.ID, task.UpdateRequest{Status: &review}); err != nil {
		t.Fatalf("review: %v", err)
	}

	// The operator closes it from the dashboard.
	rec = h.Do(h.AuthedRequest(http.MethodPatch, "/api/v1/tasks/"+created.Key, map[string]any{"status": "done"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("done: %d %s", rec.Code, rec.Body.String())
	}
	var done taskJSON
	DecodeJSON(t, rec, &done)
	if done.Status != task.StatusDone || done.Holder != "" || done.Assignee != agentID {
		t.Errorf("done = %+v", done)
	}

	h.FlushAudit(t)
	events, _, err := h.AuditStore.List(ctx, audit.ListOpts{Categories: []string{audit.CategoryTask}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]int{}
	for _, e := range events {
		actions[e.Action]++
	}
	for _, want := range []string{task.EventCreated, task.EventAssigned, task.EventClaimed, task.EventComment, task.EventStatus} {
		if actions[want] == 0 {
			t.Errorf("no %q audit event; got %v", want, actions)
		}
	}
	if actions[task.EventStatus] != 2 {
		t.Errorf("status events = %d, want 2 (review, done)", actions[task.EventStatus])
	}
}

func TestTasks_AgentCannotSkipReview(t *testing.T) {
	h := NewHarness(t, nil)
	ctx := context.Background()
	agentID := task.AgentID("default")

	tk, err := h.Tasks.Create(ctx, task.Human, task.CreateRequest{Title: "Summarise spending", Assignee: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Tasks.Checkout(ctx, agentID, tk.ID); err != nil {
		t.Fatal(err)
	}
	doneStatus := task.StatusDone
	_, err = h.Tasks.Update(ctx, agentID, tk.ID, task.UpdateRequest{Status: &doneStatus})
	var re *task.RuleError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a review RuleError", err)
	}

	rec := h.Do(h.AuthedRequest(http.MethodGet, "/api/v1/tasks/"+tk.Key(), nil))
	var detail struct {
		Task taskJSON `json:"task"`
	}
	DecodeJSON(t, rec, &detail)
	if detail.Task.Status != task.StatusInProgress || detail.Task.Holder != agentID {
		t.Errorf("task after refused close = %+v", detail.Task)
	}
}
