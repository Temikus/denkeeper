package main

import (
	"context"
	"testing"

	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/task"
)

func TestTaskDirectory_AgentsAndTaskKeys(t *testing.T) {
	ctx := context.Background()
	ks, err := api.NewInMemoryKeyStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ks.Create(ctx, "claude-code", []string{"tasks:read", "tasks:write"}); err != nil {
		t.Fatal(err)
	}
	// The dashboard key lists tasks scopes too, since REST matches scopes
	// exactly, but it is the operator, not a member.
	if _, _, err := ks.Create(ctx, "dashboard", []string{"admin", "tasks:read", "tasks:write"}); err != nil {
		t.Fatal(err)
	}
	gone, _, err := ks.Create(ctx, "old-harness", []string{"tasks:write"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ks.Revoke(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	holder := config.NewHolder(&config.Config{API: config.APIConfig{Keys: []config.APIKeyConfig{
		{Name: "opencode", Scopes: []string{"tasks:write"}},
		{Name: "claude-code", Scopes: []string{"tasks:write"}}, // same name as the stored key
	}}})

	d := &taskDirectory{cfg: holder}
	// Before main swaps in the real dispatcher, the directory has no agents.
	if ms, err := d.Members(ctx); err != nil || len(ms) != 2 {
		t.Fatalf("members before binding = %+v, %v; want only the two TOML keys", ms, err)
	}
	d.setDispatcher(testDispatcher(t, "pamela", nil))
	d.setKeyStore(ks)

	ms, err := d.Members(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	want := []string{"agent:pamela", "ext:claude-code", "ext:opencode"}
	if len(ids) != len(want) {
		t.Fatalf("members = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("member %d = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestNewTaskService_FollowsFlag(t *testing.T) {
	st, err := task.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if svc := newTaskService(&config.Config{}, st, nil, nil); svc != nil {
		t.Error("service built with [tasks] off; want nil so tasks stay hidden")
	}
	on := &config.Config{Tasks: config.TasksConfig{Enabled: true}}
	if svc := newTaskService(on, st, nil, nil); svc == nil {
		t.Error("no service with [tasks] enabled = true")
	}
}
