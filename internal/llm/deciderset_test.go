package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// decisionMockProvider is a chat provider that also serves decisions, like an
// openrouter instance in a ProviderSet.
type decisionMockProvider struct {
	mockProvider
	stubDecisionProvider
}

func newDecisionMock(name string, p float64) *decisionMockProvider {
	return &decisionMockProvider{
		mockProvider:         mockProvider{name: name},
		stubDecisionProvider: stubDecisionProvider{resp: noulResponse(p, 0.0001)},
	}
}

func liveConfig() DeciderConfig {
	return DeciderConfig{Name: "jev", Provider: "openrouter", Model: "typesafe/jev-1.13", Timeout: time.Second}
}

func TestDecider_Decide_UsesProviderPutAfterBuild(t *testing.T) {
	set := NewProviderSet()
	old := newDecisionMock("openrouter", 0.9)
	set.Put(old)
	d := NewLiveDecider(liveConfig(), set, nil)

	rotated := newDecisionMock("openrouter", 0.2)
	set.Put(rotated)

	resp, err := d.Decide(context.Background(), "s", map[string]string{"k": "v"}, noulQuestions())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if old.calls != 0 || rotated.calls != 1 {
		t.Errorf("calls old=%d rotated=%d, want 0 and 1", old.calls, rotated.calls)
	}
	if got := resp.Answers["safe"].Noul; got != 0.2 {
		t.Errorf("answer = %v, want the rotated provider's 0.2", got)
	}
}

func TestDecider_Decide_MissingProviderIsProviderError(t *testing.T) {
	d := NewLiveDecider(liveConfig(), NewProviderSet(), nil)

	_, err := d.Decide(context.Background(), "s", "state", noulQuestions())
	if !errors.Is(err, ErrNoDecisionProvider) {
		t.Fatalf("err = %v, want ErrNoDecisionProvider", err)
	}
	if got := DecisionErrorCause(err); got != "provider_error" {
		t.Errorf("cause = %q, want provider_error", got)
	}
}

func TestDecider_Decide_ChatOnlyProviderIsProviderError(t *testing.T) {
	set := NewProviderSet()
	set.Put(&mockProvider{name: "openrouter"})
	d := NewLiveDecider(liveConfig(), set, nil)

	_, err := d.Decide(context.Background(), "s", "state", noulQuestions())
	if !errors.Is(err, ErrNoDecisionProvider) {
		t.Fatalf("err = %v, want ErrNoDecisionProvider", err)
	}
}

func TestDeciderSet_SyncAddsReplacesRemoves(t *testing.T) {
	s := NewDeciderSet(NewProviderSet(), nil)
	keep := DeciderConfig{Name: "keep", Provider: "openrouter", Model: "m1"}
	change := DeciderConfig{Name: "change", Provider: "openrouter", Model: "m1"}
	s.Sync([]DeciderConfig{keep, change, {Name: "drop", Provider: "openrouter", Model: "m1"}})
	keptBefore, changedBefore := s.Get("keep"), s.Get("change")

	change.Model = "m2"
	s.Sync([]DeciderConfig{keep, change, {Name: "add", Provider: "openrouter", Model: "m1"}})

	if s.Get("keep") != keptBefore {
		t.Error("unchanged entry was rebuilt; consumers would see a spurious change")
	}
	if got := s.Get("change"); got == changedBefore || got.Model() != "m2" {
		t.Errorf("changed entry = %v (model %q), want a new decider on m2", got, got.Model())
	}
	if s.Get("drop") != nil {
		t.Error("removed entry still registered")
	}
	if s.Get("add") == nil {
		t.Error("added entry not registered")
	}
	if got := s.Names(); len(got) != 3 || got[0] != "add" || got[1] != "change" || got[2] != "keep" {
		t.Errorf("Names = %v, want [add change keep]", got)
	}
}

func TestDeciderSet_SyncedDeciderResolvesLive(t *testing.T) {
	providers := NewProviderSet()
	s := NewDeciderSet(providers, nil)
	s.Sync([]DeciderConfig{liveConfig()})

	providers.Put(newDecisionMock("openrouter", 0.7))

	if _, err := s.Get("jev").Decide(context.Background(), "s", "state", noulQuestions()); err != nil {
		t.Fatalf("a provider added after Sync was not used: %v", err)
	}
}

func TestDeciderSet_GetOnNilOrEmptyName(t *testing.T) {
	var s *DeciderSet
	if s.Get("jev") != nil || NewDeciderSet(nil, nil).Get("") != nil {
		t.Error("Get on a nil set or an empty name must return nil")
	}
}
