package config

import (
	"sync"
	"testing"
)

func TestHolder_UpdateDoesNotMutateEarlierSnapshot(t *testing.T) {
	h := NewHolder(&Config{
		DataDir: "/one",
		Agents:  []AgentInstanceConfig{{Name: "default", Description: "before"}},
		Tools:   map[string]ToolConfig{"a": {Command: "one"}},
	})
	before := h.Get()

	h.Update(func(c *Config) {
		c.DataDir = "/two"
		c.Agents[0].Description = "after"
		c.Agents = append(c.Agents, AgentInstanceConfig{Name: "second"})
		c.Tools["a"] = ToolConfig{Command: "two"}
	})

	if before.DataDir != "/one" {
		t.Errorf("earlier snapshot DataDir = %q, want /one", before.DataDir)
	}
	if before.Agents[0].Description != "before" {
		t.Errorf("earlier snapshot agent description = %q, want before", before.Agents[0].Description)
	}
	if len(before.Agents) != 1 {
		t.Errorf("earlier snapshot agent count = %d, want 1", len(before.Agents))
	}
	if before.Tools["a"].Command != "one" {
		t.Errorf("earlier snapshot tool command = %q, want one", before.Tools["a"].Command)
	}

	after := h.Get()
	if after.DataDir != "/two" || after.Agents[0].Description != "after" || len(after.Agents) != 2 {
		t.Errorf("published snapshot did not take the update: %+v", after)
	}
}

func TestHolder_ClonePointerFieldsAreIndependent(t *testing.T) {
	enabled := true
	h := NewHolder(&Config{Web: WebConfig{Enabled: &enabled}})
	before := h.Get()

	h.Update(func(c *Config) { *c.Web.Enabled = false })

	if !*before.Web.Enabled {
		t.Error("earlier snapshot's *bool was mutated through the clone")
	}
	if *h.Get().Web.Enabled {
		t.Error("update did not take on the published snapshot")
	}
}

func TestHolder_ConcurrentUpdatesDoNotLoseWrites(t *testing.T) {
	h := NewHolder(&Config{})
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Update(func(c *Config) { c.Agents = append(c.Agents, AgentInstanceConfig{Name: "a"}) })
			_ = h.Get().Agents
		}()
	}
	wg.Wait()

	if got := len(h.Get().Agents); got != n {
		t.Errorf("agent count = %d, want %d — a read-modify-write was lost", got, n)
	}
}

func TestHolder_NilSafe(t *testing.T) {
	var h *Holder
	if h.Get() != nil {
		t.Error("nil holder Get() should return nil")
	}
	h.Store(&Config{})
	if h.Update(func(*Config) { t.Error("fn must not run on a nil holder") }) != nil {
		t.Error("nil holder Update() should return nil")
	}

	empty := NewHolder(nil)
	if empty.Get() != nil {
		t.Error("holder of nil should Get() nil")
	}
	if empty.Update(func(*Config) { t.Error("fn must not run without a config") }) != nil {
		t.Error("Update on an empty holder should return nil")
	}
}
