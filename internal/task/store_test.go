package task

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	pamela = "agent:pamela"
	scout  = "agent:scout"
	claude = "ext:claude-code"
)

func newTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	s, err := NewInMemoryStore(opts...)
	if err != nil {
		t.Fatalf("NewInMemoryStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustCreate(t *testing.T, s *Store, in CreateInput) *Task {
	t.Helper()
	if in.Actor == "" {
		in.Actor = Human
	}
	if in.Title == "" {
		in.Title = "Add Fastmail to the weekly digest"
	}
	tk, err := s.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tk
}

func eventKinds(t *testing.T, s *Store, id int64) []string {
	t.Helper()
	d, err := s.Detail(context.Background(), id)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	kinds := make([]string, len(d.Events))
	for i, e := range d.Events {
		kinds[i] = e.Kind
	}
	return kinds
}

func wantRuleErr(t *testing.T, err error, substr string) {
	t.Helper()
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want RuleError", err)
	}
	if !strings.Contains(re.Message, substr) {
		t.Errorf("message %q does not contain %q", re.Message, substr)
	}
}

func TestCreate_TopLevel(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{Body: "Gmail only today", Assignee: pamela, Reviewer: Human})

	if tk.Key() != "DK-1" || tk.Status != StatusTodo || tk.Assignee != pamela || tk.Reviewer != Human {
		t.Errorf("task = %+v", tk)
	}
	if tk.Depth != 0 || tk.ParentID != nil || tk.Holder != "" || tk.ClosedAt != nil {
		t.Errorf("new task carries state it shouldn't: %+v", tk)
	}
	if got := eventKinds(t, s, tk.ID); len(got) != 1 || got[0] != EventCreated {
		t.Errorf("events = %v, want [created]", got)
	}
}

func TestCreate_SubtaskRecordsOnParent(t *testing.T) {
	s := newTestStore(t)
	parent := mustCreate(t, s, CreateInput{Assignee: pamela})
	child := mustCreate(t, s, CreateInput{Actor: pamela, Title: "Check rate limits", Assignee: claude, ParentID: &parent.ID})

	if child.Depth != 1 || child.ParentID == nil || *child.ParentID != parent.ID {
		t.Errorf("child = %+v", child)
	}
	got, _ := s.Get(context.Background(), parent.ID)
	if got.ChildrenTotal != 1 || got.ChildrenDone != 0 {
		t.Errorf("parent counts = %d/%d, want 1/0", got.ChildrenDone, got.ChildrenTotal)
	}
	if kinds := eventKinds(t, s, parent.ID); kinds[len(kinds)-1] != EventSubtask {
		t.Errorf("parent events = %v, want a trailing subtask event", kinds)
	}
}

func TestCreate_DepthCapRefused(t *testing.T) {
	s := newTestStore(t, WithMaxDepth(2))
	a := mustCreate(t, s, CreateInput{})
	b := mustCreate(t, s, CreateInput{ParentID: &a.ID})
	c := mustCreate(t, s, CreateInput{ParentID: &b.ID})

	_, err := s.Create(context.Background(), CreateInput{Actor: Human, Title: "too deep", ParentID: &c.ID})
	wantRuleErr(t, err, "levels deep")
}

func TestCreate_MaxChildrenRefused(t *testing.T) {
	s := newTestStore(t, WithMaxChildren(1))
	p := mustCreate(t, s, CreateInput{})
	mustCreate(t, s, CreateInput{ParentID: &p.ID})

	_, err := s.Create(context.Background(), CreateInput{Actor: Human, Title: "one more", ParentID: &p.ID})
	wantRuleErr(t, err, "the limit")
}

func TestCreate_ClosedParentRefused(t *testing.T) {
	s := newTestStore(t)
	p := mustCreate(t, s, CreateInput{})
	if _, err := s.SetStatus(context.Background(), Human, p.ID, StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create(context.Background(), CreateInput{Actor: Human, Title: "late", ParentID: &p.ID})
	wantRuleErr(t, err, "can't take new subtasks")
}

func TestCreate_MissingParent(t *testing.T) {
	s := newTestStore(t)
	missing := int64(99)
	_, err := s.Create(context.Background(), CreateInput{Actor: Human, Title: "orphan", ParentID: &missing})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCheckout_UnassignedTakesAssignee(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{})

	got, err := s.Checkout(context.Background(), scout, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Holder != scout || got.Assignee != scout || got.Status != StatusInProgress || got.HeldAt == nil {
		t.Errorf("after checkout = %+v", got)
	}
}

func TestCheckout_HeldByOtherConflict(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{})
	if _, err := s.Checkout(context.Background(), pamela, tk.ID); err != nil {
		t.Fatal(err)
	}

	_, err := s.Checkout(context.Background(), scout, tk.ID)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want ConflictError", err)
	}
	if ce.Holder != pamela || !strings.Contains(ce.Message, "held by pamela") || !strings.Contains(ce.Message, "Don't retry") {
		t.Errorf("conflict = %+v", ce)
	}
}

func TestCheckout_AssignedToOtherConflict(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{Assignee: claude})

	_, err := s.Checkout(context.Background(), scout, tk.ID)
	var ce *ConflictError
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "assigned to claude-code") {
		t.Fatalf("err = %v, want assigned-to conflict", err)
	}
}

func TestCheckout_ClosedConflict(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{})
	if _, err := s.SetStatus(context.Background(), Human, tk.ID, StatusCancelled, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.Checkout(context.Background(), scout, tk.ID)
	var ce *ConflictError
	if !errors.As(err, &ce) || !strings.Contains(ce.Message, "cancelled") {
		t.Fatalf("err = %v, want closed conflict", err)
	}
}

func TestCheckout_ReclaimIsNoop(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{Assignee: pamela})
	for range 2 {
		if _, err := s.Checkout(context.Background(), pamela, tk.ID); err != nil {
			t.Fatal(err)
		}
	}
	claims := 0
	for _, k := range eventKinds(t, s, tk.ID) {
		if k == EventClaimed {
			claims++
		}
	}
	if claims != 1 {
		t.Errorf("claimed events = %d, want 1", claims)
	}
}

func TestCheckout_ConcurrentOnlyOneWins(t *testing.T) {
	// File-backed so the racers really use separate connections.
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	tk := mustCreate(t, s, CreateInput{})

	const racers = 12
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Checkout(context.Background(), AgentID(string(rune('a'+i))), tk.ID)
			var ce *ConflictError
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.As(err, &ce):
				conflicts++
			default:
				t.Errorf("racer %d: unexpected error %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 || conflicts != racers-1 {
		t.Errorf("wins=%d conflicts=%d, want 1 and %d", wins, conflicts, racers-1)
	}
}

func TestSetStatus_ReviewGatesDone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Assignee: pamela, Reviewer: Human})
	if _, err := s.Checkout(ctx, pamela, tk.ID); err != nil {
		t.Fatal(err)
	}

	_, err := s.SetStatus(ctx, pamela, tk.ID, StatusDone, "")
	wantRuleErr(t, err, "needs review by human")

	got, err := s.SetStatus(ctx, pamela, tk.ID, StatusReview, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Holder != "" || got.HeldAt != nil {
		t.Errorf("review kept the hold: %+v", got)
	}
	got, err = s.SetStatus(ctx, Human, tk.ID, StatusDone, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone || got.ClosedAt == nil {
		t.Errorf("after done = %+v", got)
	}
}

func TestSetStatus_AgentReviewerCloses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Actor: pamela, Assignee: claude, Reviewer: pamela})
	if _, err := s.Checkout(ctx, claude, tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(ctx, claude, tk.ID, StatusReview, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.SetStatus(ctx, scout, tk.ID, StatusDone, "")
	wantRuleErr(t, err, "only the reviewer (pamela)")
	if _, err := s.SetStatus(ctx, pamela, tk.ID, StatusDone, ""); err != nil {
		t.Errorf("reviewer close: %v", err)
	}
}

func TestSetStatus_NoReviewerAssigneeCloses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Assignee: scout})
	if _, err := s.SetStatus(ctx, scout, tk.ID, StatusDone, ""); err != nil {
		t.Errorf("assignee close without reviewer: %v", err)
	}
}

func TestSetStatus_BlockedNeedsReason(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Assignee: pamela})

	_, err := s.SetStatus(ctx, pamela, tk.ID, StatusBlocked, "")
	wantRuleErr(t, err, "pass a reason")

	got, err := s.SetStatus(ctx, pamela, tk.ID, StatusBlocked, "needs an app password")
	if err != nil {
		t.Fatal(err)
	}
	if got.BlockedReason != "needs an app password" {
		t.Errorf("blocked_reason = %q", got.BlockedReason)
	}
	// Claiming again unblocks and clears the reason.
	got, err = s.Checkout(ctx, pamela, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInProgress || got.BlockedReason != "" {
		t.Errorf("after re-claim = %+v", got)
	}
}

func TestSetStatus_InProgressNeedsCheckout(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{Assignee: pamela})
	_, err := s.SetStatus(context.Background(), pamela, tk.ID, StatusInProgress, "")
	wantRuleErr(t, err, "task_checkout")
}

func TestSetStatus_OnlyHumanReopens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Actor: pamela, Assignee: pamela})
	if _, err := s.SetStatus(ctx, pamela, tk.ID, StatusDone, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.SetStatus(ctx, pamela, tk.ID, StatusTodo, "")
	wantRuleErr(t, err, "Only the operator can reopen")

	got, err := s.SetStatus(ctx, Human, tk.ID, StatusTodo, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ClosedAt != nil || got.Status != StatusTodo {
		t.Errorf("after reopen = %+v", got)
	}
}

func TestSetStatus_CancelOnlyFiler(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Actor: pamela, Assignee: scout})
	_, err := s.SetStatus(ctx, scout, tk.ID, StatusCancelled, "")
	wantRuleErr(t, err, "only the filer")
	if _, err := s.SetStatus(ctx, pamela, tk.ID, StatusCancelled, ""); err != nil {
		t.Errorf("filer cancel: %v", err)
	}
}

func TestSetStatus_UnknownStatus(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{})
	_, err := s.SetStatus(context.Background(), Human, tk.ID, "paused", "")
	wantRuleErr(t, err, "unknown status")
}

func TestRelease_HolderOrHuman(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Assignee: claude})
	if _, err := s.Checkout(ctx, claude, tk.ID); err != nil {
		t.Fatal(err)
	}

	_, err := s.Release(ctx, scout, tk.ID)
	wantRuleErr(t, err, "Only they or the operator")

	got, err := s.Release(ctx, Human, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Holder != "" || got.Status != StatusTodo || got.Assignee != claude {
		t.Errorf("after release = %+v (assignee should stay)", got)
	}
	_, err = s.Release(ctx, Human, tk.ID)
	wantRuleErr(t, err, "isn't held")
}

func TestAssign_HandoffClearsHold(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Assignee: pamela})
	if _, err := s.Checkout(ctx, pamela, tk.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Assign(ctx, pamela, tk.ID, scout)
	if err != nil {
		t.Fatal(err)
	}
	if got.Assignee != scout || got.Holder != "" || got.Status != StatusTodo {
		t.Errorf("after handoff = %+v", got)
	}
}

func TestAssign_StrangerRefused(t *testing.T) {
	s := newTestStore(t)
	tk := mustCreate(t, s, CreateInput{Assignee: pamela})
	_, err := s.Assign(context.Background(), scout, tk.ID, scout)
	wantRuleErr(t, err, "Comment instead")
}

func TestEdit_OnlyFilerOrHuman(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Actor: pamela})
	_, err := s.Edit(ctx, scout, tk.ID, "new title", "")
	wantRuleErr(t, err, "only the operator or the filer")

	got, err := s.Edit(ctx, pamela, tk.ID, "Add Fastmail and iCloud", "both inboxes")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Add Fastmail and iCloud" || got.Body != "both inboxes" {
		t.Errorf("after edit = %+v", got)
	}
}

func TestComment_ThreadInOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := mustCreate(t, s, CreateInput{Assignee: pamela})
	if _, err := s.Checkout(ctx, pamela, tk.ID); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Comment(ctx, pamela, tk.ID, "Plan: Scout compares JMAP and IMAP.")
	if err != nil {
		t.Fatal(err)
	}
	if ev.Kind != EventComment || ev.Actor != pamela || ev.ID == 0 {
		t.Errorf("event = %+v", ev)
	}
	want := []string{EventCreated, EventClaimed, EventComment}
	if got := eventKinds(t, s, tk.ID); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestComment_MissingTask(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Comment(context.Background(), Human, 42, "hello")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestList_FiltersAndClosedWindow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mustCreate(t, s, CreateInput{Title: "open for pamela", Assignee: pamela})
	b := mustCreate(t, s, CreateInput{Title: "done", Assignee: scout})
	mustCreate(t, s, CreateInput{Title: "child", ParentID: &a.ID})
	if _, err := s.SetStatus(ctx, Human, b.ID, StatusDone, ""); err != nil {
		t.Fatal(err)
	}

	all, err := s.List(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Title != "child" {
		t.Errorf("all = %d tasks, first %q; want 3, newest first", len(all), all[0].Title)
	}
	open, _ := s.List(ctx, Filter{ClosedSince: time.Now().Add(time.Hour)})
	if len(open) != 2 {
		t.Errorf("open-only = %d, want 2", len(open))
	}
	mine, _ := s.List(ctx, Filter{Assignee: pamela})
	if len(mine) != 1 || mine[0].ID != a.ID || mine[0].ChildrenTotal != 1 {
		t.Errorf("pamela's = %+v", mine)
	}
	top, _ := s.List(ctx, Filter{TopLevel: true})
	if len(top) != 2 {
		t.Errorf("top-level = %d, want 2", len(top))
	}
	kids, _ := s.List(ctx, Filter{ParentID: &a.ID})
	if len(kids) != 1 || kids[0].Title != "child" {
		t.Errorf("children = %+v", kids)
	}
	done, _ := s.List(ctx, Filter{Statuses: []string{StatusDone, StatusCancelled}})
	if len(done) != 1 || done[0].ID != b.ID {
		t.Errorf("done = %+v", done)
	}
}

func TestDetail_ParentAndChildren(t *testing.T) {
	s := newTestStore(t)
	p := mustCreate(t, s, CreateInput{Title: "parent"})
	c := mustCreate(t, s, CreateInput{Title: "child", ParentID: &p.ID})

	d, err := s.Detail(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Parent == nil || d.Parent.ID != p.ID {
		t.Errorf("parent = %+v", d.Parent)
	}
	pd, _ := s.Detail(context.Background(), p.ID)
	if len(pd.Children) != 1 || pd.Children[0].ID != c.ID {
		t.Errorf("children = %+v", pd.Children)
	}
}

func TestGet_NotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get(context.Background(), 7); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
