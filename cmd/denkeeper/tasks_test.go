package main

import (
	"context"
	"testing"

	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/config"
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
	if _, _, err := ks.Create(ctx, "dashboard", []string{"admin"}); err != nil {
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
