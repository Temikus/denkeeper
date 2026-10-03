package llm

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestProviderSet_PutGetRemove(t *testing.T) {
	s := NewProviderSet()
	if _, ok := s.Get("a"); ok {
		t.Fatal("empty set returned a provider")
	}
	first := &mockProvider{name: "a"}
	s.Put(first)
	s.Put(&mockProvider{name: "b"})
	if got, ok := s.Get("a"); !ok || got != first {
		t.Fatalf("Get(a) = %v, %v; want first, true", got, ok)
	}

	replacement := &mockProvider{name: "a"}
	s.Put(replacement)
	if got, _ := s.Get("a"); got != replacement {
		t.Fatal("Put did not replace the provider of the same name")
	}
	if names := s.Names(); fmt.Sprint(names) != "[a b]" {
		t.Errorf("Names() = %v, want [a b]", names)
	}

	s.Remove("a")
	s.Remove("missing")
	if _, ok := s.Get("a"); ok {
		t.Error("removed provider still present")
	}
	if snap := s.Snapshot(); len(snap) != 1 || snap["b"] == nil {
		t.Errorf("Snapshot() = %v, want only b", snap)
	}
}

func TestRouter_SharedSet_SeesLateRegistration(t *testing.T) {
	// A router built before a provider exists must route to it once it is put
	// in the shared set: this is how a provider created in the setup wizard
	// becomes usable by an agent without a restart.
	set := NewProviderSet()
	router := NewRouterWithProviders("late", "m", NewCostTracker(SessionLimits{}, nil), set)
	if router.HasProvider("late") {
		t.Fatal("provider visible before registration")
	}

	set.Put(&mockProvider{name: "late", response: &ChatResponse{Content: "hi", Model: "m"}})

	if !router.HasProvider("late") {
		t.Fatal("router does not see provider added to the shared set")
	}
	resp, err := router.Complete(context.Background(), "s1", []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q, want hi", resp.Content)
	}
}

func TestRouter_WithModelClone_SharesSet(t *testing.T) {
	set := NewProviderSet()
	router := NewRouterWithProviders("p", "m1", NewCostTracker(SessionLimits{}, nil), set)
	clone := router.WithModel("m2")

	set.Put(&mockProvider{name: "p"})

	if !clone.HasProvider("p") {
		t.Error("clone does not see a provider registered after cloning")
	}
}

func TestProviderSet_ConcurrentPutAndComplete(t *testing.T) {
	// Run with -race: replacing a provider while requests read the set must
	// not race.
	set := NewProviderSet()
	set.Put(&mockProvider{name: "p", response: &ChatResponse{Content: "ok", Model: "m"}})
	router := NewRouterWithProviders("p", "m", NewCostTracker(SessionLimits{}, nil), set)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			set.Put(&mockProvider{name: "p", response: &ChatResponse{Content: "ok", Model: "m"}})
			set.Put(&mockProvider{name: fmt.Sprintf("extra-%d", i)})
		}()
		go func() {
			defer wg.Done()
			if _, err := router.Complete(context.Background(), "s", []Message{{Role: "user", Content: "x"}}); err != nil {
				t.Errorf("Complete: %v", err)
			}
			_ = router.ListModels(context.Background())
		}()
	}
	wg.Wait()
}
