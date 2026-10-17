package configmcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/task"
)

func newTaskService(t *testing.T) *task.Service {
	t.Helper()
	store, err := task.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return task.NewService(store, nil, nil)
}

func TestTaskTools_RegisteredOnlyWithService(t *testing.T) {
	without, _ := newTestServer(t, nil)
	assertToolRegistered(t, without, "task_create", false)

	with, _ := newTestServer(t, func(d *configmcp.Deps) { d.Tasks = newTaskService(t) })
	for _, name := range []string{"task_list", "task_get", "task_create", "task_checkout", "task_comment", "task_update"} {
		assertToolRegistered(t, with, name, true)
	}
}

func TestTaskTools_ActAsTheAgent(t *testing.T) {
	svc := newTaskService(t)
	session, _ := newTestServer(t, func(d *configmcp.Deps) { d.Tasks = svc })

	text, isErr := callTool(t, session, "task_create", map[string]any{"title": "Note to self", "assignee": "me"})
	if isErr {
		t.Fatalf("task_create: %s", text)
	}
	var out struct {
		Task struct {
			Key      string `json:"key"`
			Assignee string `json:"assignee"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Task.Assignee != "agent:test-agent" {
		t.Errorf("assignee = %q, want agent:test-agent", out.Task.Assignee)
	}
	got, err := svc.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedBy != "agent:test-agent" {
		t.Errorf("created_by = %q", got.CreatedBy)
	}
}
