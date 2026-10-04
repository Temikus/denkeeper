package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

// reviewServer is supervisedServer with an audit store holding n shadow
// reviews of pamela's echo calls by "jev", each approved by argus a second
// later. The newest review is an hour old.
func reviewServer(t *testing.T, n int) *Server {
	t.Helper()
	s := supervisedServer(t)
	store, err := audit.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	newest := time.Now().UTC().Add(-time.Hour)
	for i := range n {
		at := newest.Add(-time.Duration(n-1-i) * time.Minute)
		args := fmt.Sprintf(`{"n":%d}`, i)
		for _, ev := range []audit.Event{{
			Timestamp: at, Category: audit.CategorySupervisor, Action: "review", Agent: "pamela",
			Status: audit.StatusOK, Source: "decider:jev", ConversationID: "c1",
			Detail: `{"tool":"echo","arguments":` + fmt.Sprintf("%q", args) + `,"decision":"shadow","model":"typesafe/jev-1.13","cost":0.0001,` +
				`"answers":{"aligned":{"type":"noul","noul":0.99},"safe_args":{"type":"noul","noul":0.97},"scoped":{"type":"noul","noul":0.98}}}`,
		}, {
			Timestamp: at.Add(time.Second), Category: audit.CategorySupervisor, Action: "review", Agent: "pamela",
			Status: audit.StatusOK, Source: "supervisor:argus", ConversationID: "c1",
			Detail: `{"tool":"echo","arguments":` + fmt.Sprintf("%q", args) + `,"decision":"APPROVE","supervisor":"argus","cost":0.04}`,
		}} {
			if err := store.Insert(context.Background(), ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.deps.AuditStore = store
	return s
}

func deciderReviews(t *testing.T, s *Server, in deciderReviewsInput) deciderReviewsOutput {
	t.Helper()
	res, _, err := s.handleDeciderReviews(auditReadCtx(), nil, in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", toolResultText(res))
	}
	var out deciderReviewsOutput
	decodeToolJSON(t, toolResultText(res), &out)
	return out
}

func deciderReviewsError(t *testing.T, s *Server, ctx context.Context, in deciderReviewsInput) string {
	t.Helper()
	res, _, err := s.handleDeciderReviews(ctx, nil, in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a tool error, got %s", toolResultText(res))
	}
	return toolResultText(res)
}

func TestDeciderReviews_PairsStoredReviews(t *testing.T) {
	s := reviewServer(t, 1)

	out := deciderReviews(t, s, deciderReviewsInput{Agent: "pamela", Decider: "jev"})
	if out.Decider != "jev" || out.Supervisor != "argus" || out.Total != 1 || len(out.Reviews) != 1 {
		t.Fatalf("out = %+v, want jev's one review under argus", out)
	}
	r := out.Reviews[0]
	if r.Supervisor != "APPROVE" || r.MinScore == nil || *r.MinScore != 0.97 || r.Lowest != "safe_args" {
		t.Errorf("review = %+v, want APPROVE with min 0.97 on safe_args", r)
	}
}

func TestDeciderReviews_DefaultsToTheWiredDecider(t *testing.T) {
	s := reviewServer(t, 1)
	d := llm.NewDecider(llm.DeciderConfig{Name: "jev", Model: "typesafe/jev-1.13"}, nil, nil)
	s.deps.Dispatcher.Agent("pamela").SetSupervisorDecider(d, agent.DeciderStageConfig{Mode: "shadow"})

	out := deciderReviews(t, s, deciderReviewsInput{Agent: "pamela"})
	if out.Decider != "jev" || len(out.Reviews) != 1 {
		t.Errorf("out = %+v, want jev's one review", out)
	}
}

func TestDeciderReviews_LimitCutsNewestFirstAndTotalCountsAll(t *testing.T) {
	s := reviewServer(t, 3)

	out := deciderReviews(t, s, deciderReviewsInput{Agent: "pamela", Decider: "jev", Limit: 2})
	if out.Total != 3 || len(out.Reviews) != 2 {
		t.Fatalf("total=%d len=%d, want 3 and 2", out.Total, len(out.Reviews))
	}
	if out.Reviews[0].Arguments != `{"n":2}` || out.Reviews[1].Arguments != `{"n":1}` {
		t.Errorf("got %s, %s; want the two newest", out.Reviews[0].Arguments, out.Reviews[1].Arguments)
	}
}

func TestDeciderReviews_SinceDropsOlderReviews(t *testing.T) {
	s := reviewServer(t, 1)

	out := deciderReviews(t, s, deciderReviewsInput{Agent: "pamela", Decider: "jev",
		Since: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)})
	if out.Total != 0 || out.Reviews == nil {
		t.Errorf("out = %+v, want an empty non-nil list", out)
	}
}

func TestDeciderReviews_NoDeciderIsError(t *testing.T) {
	msg := deciderReviewsError(t, reviewServer(t, 1), auditReadCtx(), deciderReviewsInput{Agent: "pamela"})
	if !strings.Contains(msg, "no supervisor_decider") {
		t.Errorf("error = %q", msg)
	}
}

func TestDeciderReviews_BadInputIsError(t *testing.T) {
	s := reviewServer(t, 1)
	for _, in := range []deciderReviewsInput{
		{Agent: "pamela", Decider: "jev", Since: "yesterday"},
		{Agent: "pamela", Decider: "jev", Limit: -1},
		{Agent: "nobody", Decider: "jev"},
	} {
		deciderReviewsError(t, s, auditReadCtx(), in)
	}
}

func TestDeciderReviews_NoAuditStoreIsError(t *testing.T) {
	msg := deciderReviewsError(t, supervisedServer(t), auditReadCtx(), deciderReviewsInput{Agent: "pamela", Decider: "jev"})
	if msg != "audit not configured" {
		t.Errorf("error = %q", msg)
	}
}

func TestDeciderReviews_RequiresAuditScope(t *testing.T) {
	deciderReviewsError(t, reviewServer(t, 1), agentReadScope(t), deciderReviewsInput{Agent: "pamela", Decider: "jev"})
}
