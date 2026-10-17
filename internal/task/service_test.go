package task

import (
	"context"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/audit"
)

type recordingAuditor struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingAuditor) Emit(_ context.Context, e audit.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func testDirectory() Directory {
	return DirectoryFunc(func(context.Context) ([]Member, error) {
		return []Member{
			{ID: pamela, Name: "pamela", Kind: "agent"},
			{ID: scout, Name: "scout", Kind: "agent"},
			{ID: claude, Name: "claude-code", Kind: "external"},
			{ID: "ext:scout", Name: "scout", Kind: "external"},
		}, nil
	})
}

func newTestService(t *testing.T) (*Service, *recordingAuditor) {
	t.Helper()
	a := &recordingAuditor{}
	return NewService(newTestStore(t), testDirectory(), a), a
}

func strp(s string) *string { return &s }

func TestResolve_Names(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	cases := map[string]string{
		"":                "",
		"me":              pamela,
		"you":             Human,
		"Human":           Human,
		"pamela":          pamela,
		"Claude-Code":     claude,
		"ext:claude-code": claude,
		"ext:scout":       "ext:scout",
	}
	for in, want := range cases {
		got, err := svc.Resolve(ctx, in, pamela)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestResolve_AmbiguousAndUnknown(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Resolve(context.Background(), "scout", Human)
	wantRuleErr(t, err, "Use the full member ID")
	_, err = svc.Resolve(context.Background(), "quill", Human)
	wantRuleErr(t, err, `no member named "quill"`)
}

func TestServiceCreate_ReviewerDefaults(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	byHuman, err := svc.Create(ctx, Human, CreateRequest{Title: "Digest", Assignee: "pamela"})
	if err != nil {
		t.Fatal(err)
	}
	if byHuman.Reviewer != Human || byHuman.Assignee != pamela || byHuman.CreatedBy != Human {
		t.Errorf("human-filed = %+v", byHuman)
	}
	noReview, _ := svc.Create(ctx, Human, CreateRequest{Title: "Quick", Assignee: "agent:scout", NoReview: true})
	if noReview == nil {
		t.Fatal("no-review create failed")
	}
	if noReview.Reviewer != "" {
		t.Errorf("NoReview reviewer = %q", noReview.Reviewer)
	}
	self, _ := svc.Create(ctx, pamela, CreateRequest{Title: "Note to self", Assignee: "me"})
	if self.Reviewer != "" || self.Assignee != pamela {
		t.Errorf("self-assigned = %+v, want no reviewer", self)
	}
}

func TestServiceCreate_Unassigned(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	tk, err := svc.Create(ctx, Human, CreateRequest{Title: "Renew the domain"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Assignee != "" || tk.Status != StatusTodo || tk.Reviewer != Human {
		t.Errorf("unassigned = %+v", tk)
	}
	// The operator can close their own to-do without anyone claiming it.
	done, err := svc.Update(ctx, Human, tk.ID, UpdateRequest{Status: strp(StatusDone)})
	if err != nil || done.Status != StatusDone {
		t.Errorf("closing unassigned to-do: %+v, %v", done, err)
	}
}

func TestServiceCreate_UnassignedClaimable(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tk, _ := svc.Create(ctx, Human, CreateRequest{Title: "Anyone: tidy the KV namespaces"})

	got, err := svc.Checkout(ctx, scout, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Assignee != scout || got.Holder != scout {
		t.Errorf("claimed unassigned = %+v", got)
	}
}

func TestServiceUpdate_Unassign(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tk, _ := svc.Create(ctx, Human, CreateRequest{Title: "Digest", Assignee: "pamela"})

	got, err := svc.Update(ctx, Human, tk.ID, UpdateRequest{Assignee: strp("")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Assignee != "" {
		t.Errorf("assignee = %q, want unassigned", got.Assignee)
	}
}

func TestServiceCreate_Validation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	_, err := svc.Create(ctx, Human, CreateRequest{Title: "   "})
	wantRuleErr(t, err, "title is required")
	long := make([]rune, MaxTitleLen+1)
	for i := range long {
		long[i] = 'x'
	}
	_, err = svc.Create(ctx, Human, CreateRequest{Title: string(long)})
	wantRuleErr(t, err, "the limit is 200")
	_, err = svc.Create(ctx, Human, CreateRequest{Title: "ok", Assignee: "quill"})
	wantRuleErr(t, err, "no member named")
}

func TestServiceUpdate_HandoffThenStatus(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tk, _ := svc.Create(ctx, Human, CreateRequest{Title: "Digest", Assignee: "pamela"})

	got, err := svc.Update(ctx, Human, tk.ID, UpdateRequest{Assignee: strp("agent:scout"), Status: strp(StatusBlocked), Reason: "waiting on a password"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Assignee != scout || got.Status != StatusBlocked || got.BlockedReason != "waiting on a password" {
		t.Errorf("after update = %+v", got)
	}
}

func TestServiceUpdate_ValidatesBeforeWriting(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tk, _ := svc.Create(ctx, Human, CreateRequest{Title: "Digest", Assignee: "pamela"})

	_, err := svc.Update(ctx, Human, tk.ID, UpdateRequest{Title: strp("Renamed"), Assignee: strp("quill")})
	wantRuleErr(t, err, "no member named")
	got, _ := svc.Get(ctx, tk.ID)
	if got.Title != "Digest" {
		t.Errorf("title changed to %q despite the failed update", got.Title)
	}
}

func TestService_AuditAndNotify(t *testing.T) {
	svc, aud := newTestService(t)
	ctx := context.Background()
	var mu sync.Mutex
	var changes []Change
	svc.SetOnChange(func(c Change) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, c)
	})

	parent, _ := svc.Create(ctx, Human, CreateRequest{Title: "Digest", Assignee: "pamela"})
	if _, err := svc.Checkout(ctx, pamela, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, pamela, CreateRequest{Title: "Rate limits", Assignee: "claude-code", ParentID: &parent.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Comment(ctx, pamela, parent.ID, "Filed DK-2 for Claude Code."); err != nil {
		t.Fatal(err)
	}

	wantKinds := []string{EventCreated, EventClaimed, EventCreated, EventSubtask, EventComment}
	if len(changes) != len(wantKinds) {
		t.Fatalf("changes = %+v, want kinds %v", changes, wantKinds)
	}
	for i, k := range wantKinds {
		if changes[i].Kind != k {
			t.Errorf("change %d kind = %q, want %q", i, changes[i].Kind, k)
		}
	}
	if len(aud.events) != 4 {
		t.Fatalf("audit events = %d, want 4", len(aud.events))
	}
	claim := aud.events[1]
	if claim.Category != audit.CategoryTask || claim.Action != EventClaimed || claim.Agent != "pamela" || claim.Summary != "pamela claimed DK-1" {
		t.Errorf("claim audit = %+v", claim)
	}
	if aud.events[0].Agent != "" {
		t.Errorf("human action carries agent %q", aud.events[0].Agent)
	}
}

func TestServiceMembers_HumanFirst(t *testing.T) {
	svc, _ := newTestService(t)
	ms, err := svc.Members(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 5 || ms[0].ID != Human {
		t.Errorf("members = %+v", ms)
	}
}
