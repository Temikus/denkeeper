//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
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

// A headless check-in schedule runs with no chat: the agent claims its task
// and nothing is sent anywhere.
func TestTasks_HeadlessCheckInClaimsWork(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHarness(t, &HarnessOpts{
		ConfigPath:    cfgPath,
		WithTaskTools: true,
		// A supervised agent with no supervisor would have every call denied:
		// a headless run has no chat to ask in.
		Agents: []agentSetup{{Name: "default", Tier: "autonomous"}},
		Responses: []*llm.ChatResponse{
			{ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "task_list", Arguments: `{}`}}}, FinishReason: "tool_calls", Model: "test-model"},
			{ToolCalls: []llm.ToolCall{{ID: "c2", Type: "function", Function: llm.FunctionCall{Name: "task_checkout", Arguments: `{"id":"DK-1"}`}}}, FinishReason: "tool_calls", Model: "test-model"},
			{Content: "Claimed DK-1.", Model: "test-model", FinishReason: "stop"},
		},
	})
	t.Cleanup(h.Scheduler.Stop)
	ctx := context.Background()

	tk, err := h.Tasks.Create(ctx, task.Human, task.CreateRequest{Title: "Check Fastmail rate limits", Assignee: "default"})
	if err != nil {
		t.Fatal(err)
	}
	rec := h.Do(h.AuthedRequest(http.MethodPost, "/api/v1/schedules", map[string]any{
		"name": "check-tasks-default", "schedule": "@every 1s", "agent": "default",
		"session_mode": "shared", "headless": true,
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create schedule: %d %s", rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := h.Tasks.Get(ctx, tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Holder == task.AgentID("default") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("check-in never claimed the task: %+v", got)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := h.Scheduler.Unregister("check-tasks-default"); err != nil {
		t.Fatal(err)
	}

	msgs, err := h.Memory.GetMessages(ctx, "sched:check-tasks-default", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 {
		t.Fatal("no turn stored for the headless run")
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "headless = true") {
		t.Errorf("persisted schedule:\n%s", raw)
	}
}
