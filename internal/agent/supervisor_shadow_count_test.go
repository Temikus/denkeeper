package agent

import (
	"context"
	"encoding/json"
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
