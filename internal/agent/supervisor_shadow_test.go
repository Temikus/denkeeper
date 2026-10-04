package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

var shadowT0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func shadowEvent(t *testing.T, at time.Duration, conv, tool, args string, scores map[string]float64) audit.Event {
	t.Helper()
	answers := map[string]llm.Answer{}
	for id, p := range scores {
		answers[id] = llm.Answer{Type: llm.QuestionNoul, Noul: p}
	}
	detail, _ := json.Marshal(map[string]any{
		"stage": "decider", "mode": "shadow", "tool": tool, "arguments": args, "decider": "jev",
		"model": "typesafe/jev-1.13", "decision": "shadow", "would_decide": "APPROVE",
		"answers": answers, "cost": 0.0001,
	})
	return audit.Event{
		Timestamp: shadowT0.Add(at), Category: audit.CategorySupervisor, Action: "review", Agent: "pamela",
		Status: audit.StatusOK, Source: "decider:jev", ConversationID: conv, Detail: string(detail),
	}
}

func supervisorEvent(t *testing.T, at time.Duration, conv, tool, args, decision string, cost *float64) audit.Event {
	t.Helper()
	fields := map[string]any{"tool": tool, "arguments": args, "decision": decision, "supervisor": "argus"}
	if cost != nil {
		fields["cost"] = *cost
	}
	detail, _ := json.Marshal(fields)
	return audit.Event{
		Timestamp: shadowT0.Add(at), Category: audit.CategorySupervisor, Action: "review", Agent: "pamela",
		Status: audit.StatusOK, Source: "supervisor:argus", ConversationID: conv, Detail: string(detail),
	}
}

func allScores(p float64) map[string]float64 {
	return map[string]float64{deciderQAligned: p, deciderQSafeArgs: p, deciderQScoped: p}
}

func TestPairShadowReviews_PairsWithSupervisorVerdict(t *testing.T) {
	cost := 0.04
	events := []audit.Event{
		supervisorEvent(t, 2*time.Second, "c1", "web_fetch", `{"url":"a"}`, "DENY", &cost),
		shadowEvent(t, 0, "c1", "web_fetch", `{"url":"a"}`, map[string]float64{
			deciderQAligned: 0.99, deciderQSafeArgs: 0.93, deciderQScoped: 0.97,
		}),
	}
	reviews, failed := PairShadowReviews(events, "jev")
	if failed != 0 || len(reviews) != 1 {
		t.Fatalf("got %d reviews, %d failed; want 1, 0", len(reviews), failed)
	}
	r := reviews[0]
	if r.Supervisor != "DENY" || r.SupervisorName != "argus" {
		t.Errorf("supervisor = %q/%q, want DENY/argus", r.Supervisor, r.SupervisorName)
	}
	if r.MinScore == nil || *r.MinScore != 0.93 || r.Lowest != deciderQSafeArgs {
		t.Errorf("min = %v on %q, want 0.93 on safe_args", r.MinScore, r.Lowest)
	}
	if r.SupervisorCost == nil || *r.SupervisorCost != 0.04 {
		t.Errorf("supervisor cost = %v, want 0.04", r.SupervisorCost)
	}
	if r.DeciderCost != 0.0001 || r.Model != "typesafe/jev-1.13" {
		t.Errorf("decider cost/model = %v/%q", r.DeciderCost, r.Model)
	}
}

// Two identical calls in one conversation pair first-in first-out, and the
// result lists the newest first.
func TestPairShadowReviews_DuplicateCallsPairInOrder(t *testing.T) {
	events := []audit.Event{
		shadowEvent(t, 0, "c1", "kv_set", `{}`, allScores(0.2)),
		shadowEvent(t, time.Second, "c1", "kv_set", `{}`, allScores(0.8)),
		supervisorEvent(t, 2*time.Second, "c1", "kv_set", `{}`, "ESCALATE", nil),
		supervisorEvent(t, 3*time.Second, "c1", "kv_set", `{}`, "APPROVE", nil),
	}
	reviews, _ := PairShadowReviews(events, "jev")
	if len(reviews) != 2 {
		t.Fatalf("got %d reviews, want 2", len(reviews))
	}
	if *reviews[0].MinScore != 0.8 || reviews[0].Supervisor != "APPROVE" {
		t.Errorf("newest = %v/%q, want 0.8/APPROVE", *reviews[0].MinScore, reviews[0].Supervisor)
	}
	if *reviews[1].MinScore != 0.2 || reviews[1].Supervisor != "ESCALATE" {
		t.Errorf("oldest = %v/%q, want 0.2/ESCALATE", *reviews[1].MinScore, reviews[1].Supervisor)
	}
}

func TestPairShadowReviews_DifferentArgumentsDoNotPair(t *testing.T) {
	events := []audit.Event{
		shadowEvent(t, 0, "c1", "web_fetch", `{"url":"a"}`, allScores(0.9)),
		supervisorEvent(t, time.Second, "c1", "web_fetch", `{"url":"b"}`, "APPROVE", nil),
		supervisorEvent(t, time.Second, "c2", "web_fetch", `{"url":"a"}`, "APPROVE", nil),
	}
	reviews, _ := PairShadowReviews(events, "jev")
	if len(reviews) != 1 || reviews[0].Supervisor != "" {
		t.Fatalf("reviews = %+v, want one without a supervisor verdict", reviews)
	}
}

// A failed supervisor review consumes the decider row without a verdict, so
// the next identical call pairs with its own review.
func TestPairShadowReviews_SupervisorErrorLeavesNoVerdict(t *testing.T) {
	events := []audit.Event{
		shadowEvent(t, 0, "c1", "echo", `{}`, allScores(0.5)),
		supervisorEvent(t, time.Second, "c1", "echo", `{}`, "error", nil),
		shadowEvent(t, 2*time.Second, "c1", "echo", `{}`, allScores(0.6)),
		supervisorEvent(t, 3*time.Second, "c1", "echo", `{}`, "APPROVE", nil),
	}
	reviews, _ := PairShadowReviews(events, "jev")
	if len(reviews) != 2 {
		t.Fatalf("got %d reviews, want 2", len(reviews))
	}
	if reviews[1].Supervisor != "" || reviews[1].SupervisorName != "argus" {
		t.Errorf("errored review = %q/%q, want no verdict from argus", reviews[1].Supervisor, reviews[1].SupervisorName)
	}
	if reviews[0].Supervisor != "APPROVE" {
		t.Errorf("second review = %q, want APPROVE", reviews[0].Supervisor)
	}
}

// A supervisor review long after the decider review belongs to another call.
func TestPairShadowReviews_OutsideWindowDoesNotPair(t *testing.T) {
	events := []audit.Event{
		shadowEvent(t, 0, "c1", "echo", `{}`, allScores(0.5)),
		supervisorEvent(t, shadowPairWindow+time.Minute, "c1", "echo", `{}`, "APPROVE", nil),
	}
	reviews, _ := PairShadowReviews(events, "jev")
	if len(reviews) != 1 || reviews[0].Supervisor != "" {
		t.Fatalf("reviews = %+v, want one unpaired", reviews)
	}
}

func TestPairShadowReviews_SkipsEnforceErrorsAndOtherDeciders(t *testing.T) {
	enforce := shadowEvent(t, 0, "c1", "echo", `{}`, allScores(0.99))
	enforce.Detail = strings.Replace(enforce.Detail, `"decision":"shadow"`, `"decision":"APPROVE"`, 1)
	failedEv := shadowEvent(t, time.Second, "c1", "echo", `{}`, nil)
	failedEv.Status = audit.StatusError
	other := shadowEvent(t, 2*time.Second, "c1", "echo", `{}`, allScores(0.5))
	other.Source = "decider:other"

	reviews, failed := PairShadowReviews([]audit.Event{enforce, failedEv, other}, "jev")
	if len(reviews) != 0 || failed != 1 {
		t.Fatalf("got %d reviews, %d failed; want 0, 1", len(reviews), failed)
	}
}

// A missing answer escalates in decideSupervisorOutcome, so it has no score
// that a threshold could settle.
func TestPairShadowReviews_MissingAnswerHasNoMinScore(t *testing.T) {
	events := []audit.Event{shadowEvent(t, 0, "c1", "echo", `{}`, map[string]float64{deciderQAligned: 0.99})}
	reviews, _ := PairShadowReviews(events, "jev")
	if len(reviews) != 1 || reviews[0].MinScore != nil || reviews[0].Lowest != "" {
		t.Fatalf("reviews = %+v, want one with no min score", reviews)
	}
	if reviews[0].Scores[deciderQAligned] != 0.99 {
		t.Errorf("scores = %v, want the answer that came back", reviews[0].Scores)
	}
}

// Reviews audited before the cost field report it as unknown, not as free.
func TestPairShadowReviews_MissingSupervisorCostIsNil(t *testing.T) {
	events := []audit.Event{
		shadowEvent(t, 0, "c1", "echo", `{}`, allScores(0.5)),
		supervisorEvent(t, time.Second, "c1", "echo", `{}`, "APPROVE", nil),
	}
	reviews, _ := PairShadowReviews(events, "jev")
	if reviews[0].SupervisorCost != nil {
		t.Errorf("supervisor cost = %v, want nil", *reviews[0].SupervisorCost)
	}
}

func TestPairShadowReviews_CutsLongArguments(t *testing.T) {
	args := `{"code":"` + strings.Repeat("x", 500) + `"}`
	events := []audit.Event{
		shadowEvent(t, 0, "c1", "run_javascript", args, allScores(0.5)),
		supervisorEvent(t, time.Second, "c1", "run_javascript", args, "APPROVE", nil),
	}
	reviews, _ := PairShadowReviews(events, "jev")
	if n := len([]rune(reviews[0].Arguments)); n != shadowArgsRunes+1 {
		t.Errorf("arguments are %d runes, want %d plus the ellipsis", n, shadowArgsRunes)
	}
	if reviews[0].Supervisor != "APPROVE" {
		t.Errorf("supervisor = %q: pairing must use the full arguments", reviews[0].Supervisor)
	}
}

// shadowStore returns an in-memory audit store holding n shadow reviews of
// distinct calls, each followed a second later by the supervisor's approval.
func shadowStore(t *testing.T, n int) audit.Store {
	t.Helper()
	store, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	events := make([]audit.Event, 0, 2*n)
	for i := range n {
		at := time.Duration(i) * time.Minute
		args := fmt.Sprintf(`{"n":%d}`, i)
		events = append(events,
			shadowEvent(t, at, "c1", "echo", args, allScores(0.99)),
			supervisorEvent(t, at+time.Second, "c1", "echo", args, "APPROVE", nil))
	}
	if err := store.InsertBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestLoadShadowReviews_PagesPastOneListCall(t *testing.T) {
	const n = 150 // 300 events: more than one shadowReviewsPageSize page
	store := shadowStore(t, n)

	set, err := LoadShadowReviews(context.Background(), store, "pamela", "jev", shadowT0.Add(-time.Hour), shadowT0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Reviews) != n || set.Truncated || set.Failed != 0 {
		t.Fatalf("got %d reviews, truncated=%v, failed=%d; want %d, false, 0", len(set.Reviews), set.Truncated, set.Failed, n)
	}
	for _, r := range set.Reviews {
		if r.Supervisor != "APPROVE" {
			t.Fatalf("review %s unpaired: %+v", r.Arguments, r)
		}
	}
	if !set.Reviews[0].Time.After(set.Reviews[n-1].Time) {
		t.Error("reviews should be newest first")
	}
}

func TestLoadShadowReviews_ScanCapTruncates(t *testing.T) {
	store := shadowStore(t, 150)

	set, err := loadShadowReviews(context.Background(), store, "pamela", "jev", shadowT0.Add(-time.Hour), shadowT0.Add(24*time.Hour), shadowReviewsPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Truncated {
		t.Error("expected truncated once the scan cap is hit")
	}
	if len(set.Reviews) == 0 || len(set.Reviews) >= 150 {
		t.Errorf("got %d reviews, want some but not all of 150", len(set.Reviews))
	}
}

func TestLoadShadowReviews_EmptyWindowIsNonNil(t *testing.T) {
	store := shadowStore(t, 1)

	set, err := LoadShadowReviews(context.Background(), store, "pamela", "jev", shadowT0.Add(time.Hour), shadowT0.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if set.Reviews == nil || len(set.Reviews) != 0 {
		t.Errorf("reviews = %#v, want an empty non-nil slice", set.Reviews)
	}
}
