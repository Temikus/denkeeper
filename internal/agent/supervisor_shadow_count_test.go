package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

// answersWith is every question answered 0.01 (safe), with overrides. A
// negative override leaves that question unanswered.
func answersWith(over map[string]float64) map[string]llm.Answer {
	out := map[string]llm.Answer{}
	for _, id := range deciderQuestionIDs {
		out[id] = llm.Answer{Type: llm.QuestionNoul, Noul: 0.01}
	}
	for id, p := range over {
		if p < 0 {
			delete(out, id)
			continue
		}
		out[id] = llm.Answer{Type: llm.QuestionNoul, Noul: p}
	}
	return out
}

func countShadowDetail(answers map[string]llm.Answer) string {
	b, _ := json.Marshal(map[string]any{"tool": "echo", "arguments": "{}", "decision": "shadow", "model": "m", "answers": answers})
	return string(b)
}

func TestShadowReviewVerdict_MatchesDecideSupervisorOutcome(t *testing.T) {
	cases := map[string]map[string]float64{
		"approve":        nil,
		"deny":           {"secrets": 0.99},
		"escalate":       {"off_task": 0.5},
		"missing answer": {"overreach": -1},
		"low no_leak":    {"sends_external": 0.99, "private_data": 0.99},
	}
	for name, over := range cases {
		answers := answersWith(over)
		want, _ := decideSupervisorOutcome(answers, 0.95, 0.05)
		r, ok := shadowReviewFrom(audit.Event{Detail: countShadowDetail(answers)})
		if !ok {
			t.Fatalf("%s: review not parsed", name)
		}
		if got := r.Verdict(0.95, 0.05); got != string(want) {
			t.Errorf("%s: Verdict = %s, decideSupervisorOutcome = %s", name, got, want)
		}
	}
}

// countStore holds shadow reviews of "pamela"'s echo call by jev.
type countStore struct {
	t *testing.T
	s *audit.SQLiteStore
}

func newCountStore(t *testing.T) countStore {
	t.Helper()
	s, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return countStore{t: t, s: s}
}

func (c countStore) insert(ev audit.Event) {
	c.t.Helper()
	if err := c.s.Insert(context.Background(), ev); err != nil {
		c.t.Fatal(err)
	}
}

// review stores a shadow review at `at` and, unless verdict is "", the
// supervisor's verdict on the same call at `verdictAt`.
func (c countStore) review(conv string, at time.Time, over map[string]float64, verdict string, verdictAt time.Time) {
	c.t.Helper()
	c.insert(audit.Event{Timestamp: at.UTC(), Category: audit.CategorySupervisor, Action: "review", Agent: "pamela",
		Status: audit.StatusOK, Source: "decider:jev", ConversationID: conv, Detail: countShadowDetail(answersWith(over))})
	if verdict == "" {
		return
	}
	d, _ := json.Marshal(map[string]any{"tool": "echo", "arguments": "{}", "decision": verdict, "supervisor": "argus"})
	c.insert(audit.Event{Timestamp: verdictAt.UTC(), Category: audit.CategorySupervisor, Action: "review", Agent: "pamela",
		Status: audit.StatusOK, Source: "supervisor:argus", ConversationID: conv, Detail: string(d)})
}

func (c countStore) count(after time.Time, approveAt float64) int {
	c.t.Helper()
	n, err := CountUnsafeApprovals(context.Background(), c.s, "pamela", "jev", after, approveAt, 0.05)
	if err != nil {
		c.t.Fatalf("CountUnsafeApprovals: %v", err)
	}
	return n
}

var countT = time.Now().Add(-time.Hour)

// The badge must not undercount an agent with more supervisor events than the
// reviews list's scan cap: the count reads every page.
func TestCountUnsafeApprovals_CountsPastTheReviewsScanCap(t *testing.T) {
	c := newCountStore(t)
	pairs := shadowReviewsMaxScan/2 + 1 // two events each, so the scan cap is passed
	start := time.Now().Add(-4 * time.Hour)
	verdict, _ := json.Marshal(map[string]any{"tool": "echo", "arguments": "{}", "decision": "DENY", "supervisor": "argus"})
	events := make([]audit.Event, 0, 2*pairs)
	for i := range pairs {
		at := start.Add(time.Duration(2*i) * time.Second).UTC()
		conv := fmt.Sprintf("c%d", i)
		events = append(events,
			audit.Event{Timestamp: at, Category: audit.CategorySupervisor, Action: "review", Agent: "pamela",
				Status: audit.StatusOK, Source: "decider:jev", ConversationID: conv, Detail: countShadowDetail(answersWith(nil))},
			audit.Event{Timestamp: at.Add(time.Second), Category: audit.CategorySupervisor, Action: "review", Agent: "pamela",
				Status: audit.StatusOK, Source: "supervisor:argus", ConversationID: conv, Detail: string(verdict)})
	}
	if err := c.s.InsertBatch(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if n := c.count(time.Time{}, 0.95); n != pairs {
		t.Fatalf("count = %d, want all %d", n, pairs)
	}
}

// The count scans and pairs on its own path; it must agree with the panel's
// PairShadowReviews on duplicate calls, expired reviews, supervisor errors,
// unanswered reviews and wrong denials alike.
func TestCountUnsafeApprovals_AgreesWithThePanelsPairing(t *testing.T) {
	c := newCountStore(t)
	base := time.Now().Add(-3 * time.Hour)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }
	c.review("dup", at(0), nil, "DENY", at(1))                                        // unsafe
	c.review("dup", at(2), nil, "", time.Time{})                                      // same call, waits
	c.review("dup", at(3), nil, "ESCALATE", at(4))                                    // FIFO: pairs with at(2), unsafe
	c.review("late", at(10), nil, "DENY", at(40))                                     // verdict past the window: unpaired
	c.review("err", at(50), nil, "error", at(51))                                     // supervisor failed: not unsafe
	c.review("wrong", at(60), map[string]float64{"secrets": 0.99}, "APPROVE", at(61)) // wrong denial
	c.review("fine", at(70), nil, "APPROVE", at(71))                                  // agreement
	c.review("waiting", at(80), nil, "", time.Time{})                                 // no verdict yet

	// UTC: List compares stored timestamps as text, so a local-zone bound misfilters.
	set, err := LoadShadowReviews(context.Background(), c.s, "pamela", "jev", base.Add(-time.Hour).UTC(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, r := range set.Reviews {
		if r.UnsafeApproval(0.95, 0.05) {
			want++
		}
	}
	if want != 2 {
		t.Fatalf("fixture gives the panel %d unsafe approvals, want 2", want)
	}
	if got := c.count(time.Time{}, 0.95); got != want {
		t.Fatalf("count = %d, panel pairing = %d", got, want)
	}
}

func TestCountUnsafeApprovals_DeniedAndEscalated(t *testing.T) {
	c := newCountStore(t)
	c.review("c1", countT, nil, "DENY", countT.Add(time.Second))
	c.review("c2", countT, nil, "ESCALATE", countT.Add(time.Second))
	if n := c.count(time.Time{}, 0.95); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}

func TestCountUnsafeApprovals_IgnoresWrongDenialAndMissingVerdict(t *testing.T) {
	c := newCountStore(t)
	c.review("c1", countT, map[string]float64{"secrets": 0.99}, "APPROVE", countT.Add(time.Second))
	c.review("c2", countT, nil, "", time.Time{})
	if n := c.count(time.Time{}, 0.95); n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

func TestCountUnsafeApprovals_IgnoresReviewsAtOrBeforeMark(t *testing.T) {
	c := newCountStore(t)
	c.review("c1", countT, nil, "DENY", countT.Add(time.Second))
	if n := c.count(countT.UTC(), 0.95); n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

// Review A sits before the mark and its verdict arrives after it. B, the same
// call, comes after the mark with no verdict of its own. Loading from the mark
// would pair A's verdict with B.
func TestCountUnsafeApprovals_EarlierVerdictDoesNotPairAcrossMark(t *testing.T) {
	c := newCountStore(t)
	mark := countT.Add(30 * time.Second)
	c.review("c1", countT, nil, "DENY", countT.Add(60*time.Second))
	c.review("c1", countT.Add(40*time.Second), nil, "", time.Time{})
	if n := c.count(mark, 0.95); n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

func TestCountUnsafeApprovals_HigherApproveAtDropsCount(t *testing.T) {
	c := newCountStore(t)
	c.review("c1", countT, map[string]float64{"off_task": 0.03}, "DENY", countT.Add(time.Second)) // aligned 0.97
	if n := c.count(time.Time{}, 0.95); n != 1 {
		t.Fatalf("count at 0.95 = %d, want 1", n)
	}
	if n := c.count(time.Time{}, 0.99); n != 0 {
		t.Fatalf("count at 0.99 = %d, want 0", n)
	}
}

func TestCountUnsafeApprovals_ZeroMarkLooksBack30Days(t *testing.T) {
	c := newCountStore(t)
	old := time.Now().Add(-31 * 24 * time.Hour)
	c.review("c1", old, nil, "DENY", old.Add(time.Second))
	c.review("c2", countT, nil, "DENY", countT.Add(time.Second))
	if n := c.count(time.Time{}, 0.95); n != 1 {
		t.Fatalf("count = %d, want 1 (the 31-day-old review is out)", n)
	}
}
