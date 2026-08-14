package agent

import (
	"context"
	"testing"
)

func revisionStore(t *testing.T) *SQLiteMemoryStore {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func strPtr(s string) *string { return &s }

func appendRev(t *testing.T, store *SQLiteMemoryStore, rev SkillRevision) int64 {
	t.Helper()
	id, err := store.AppendSkillRevision(context.Background(), rev)
	if err != nil {
		t.Fatalf("appending revision: %v", err)
	}
	return id
}

// TestSkillRevisions_AppendAssignsSeqPerTransition proves seq is the store's
// job: callers supply only a transition id, and numbering restarts per
// transition so LIFO order is well defined within each edit.
func TestSkillRevisions_AppendAssignsSeqPerTransition(t *testing.T) {
	store := revisionStore(t)
	ctx := context.Background()

	appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t1", Op: SkillOpCreate, SkillName: "one", Actor: SkillActorSelf})
	appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t1", Op: SkillOpUpdate, SkillName: "one", PriorPayload: strPtr("x"), Actor: SkillActorSelf})
	appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t2", Op: SkillOpCreate, SkillName: "two", Actor: SkillActorSelf})

	t1, err := store.TransitionSkillRevisions(ctx, "a", "t1")
	if err != nil {
		t.Fatalf("listing t1: %v", err)
	}
	if len(t1) != 2 {
		t.Fatalf("t1 has %d revisions, want 2", len(t1))
	}
	// LIFO: newest (seq 2) first.
	if t1[0].Seq != 2 || t1[1].Seq != 1 {
		t.Errorf("seq order = %d, %d; want 2, 1", t1[0].Seq, t1[1].Seq)
	}

	t2, err := store.TransitionSkillRevisions(ctx, "a", "t2")
	if err != nil {
		t.Fatalf("listing t2: %v", err)
	}
	if len(t2) != 1 || t2[0].Seq != 1 {
		t.Errorf("t2 numbering did not restart: %+v", t2)
	}
}

// TestSkillRevisions_RoundTripsNullableFields proves the columns that carry
// NULL keep carrying it: a create must be distinguishable from an update of an
// empty file, or the reverter cannot tell "delete" from "restore nothing".
func TestSkillRevisions_RoundTripsNullableFields(t *testing.T) {
	store := revisionStore(t)
	ctx := context.Background()

	createID := appendRev(t, store, SkillRevision{
		Agent: "a", TransitionID: "t1", Op: SkillOpCreate, SkillName: "greet",
		NewVersion: "1.0.0", Actor: SkillActorUser,
	})
	renameID := appendRev(t, store, SkillRevision{
		Agent: "a", TransitionID: "t2", Op: SkillOpRename, SkillName: "new",
		PriorName: strPtr("old"), PriorPayload: strPtr("prior bytes"),
		PriorVersion: "1.0.0", NewVersion: "2.0.0", Actor: SkillActorSelf,
	})

	created, err := store.GetSkillRevision(ctx, createID)
	if err != nil {
		t.Fatalf("get create: %v", err)
	}
	if created.PriorPayload != nil || created.PriorName != nil {
		t.Errorf("create must keep NULL prior fields: %+v", created)
	}
	if created.PriorVersion != "" || created.NewVersion != "1.0.0" {
		t.Errorf("versions = (%q, %q), want ('', 1.0.0)", created.PriorVersion, created.NewVersion)
	}
	if created.RevertedAt != nil {
		t.Error("a fresh revision must be armed")
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at was not populated")
	}

	renamed, err := store.GetSkillRevision(ctx, renameID)
	if err != nil {
		t.Fatalf("get rename: %v", err)
	}
	if renamed.PriorName == nil || *renamed.PriorName != "old" {
		t.Errorf("prior name not round-tripped: %+v", renamed.PriorName)
	}
	if renamed.PriorPayload == nil || *renamed.PriorPayload != "prior bytes" {
		t.Errorf("prior payload not round-tripped: %+v", renamed.PriorPayload)
	}
}

// TestSkillRevisions_GetSkillRevision_NotFound returns nothing rather than an
// error, matching GetSkillUsage.
func TestSkillRevisions_GetSkillRevision_NotFound(t *testing.T) {
	store := revisionStore(t)

	rev, err := store.GetSkillRevision(context.Background(), 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rev != nil {
		t.Errorf("expected nil for a missing revision, got %+v", rev)
	}
}

// TestSkillRevisions_LatestSkipsDisposed proves "latest" means latest *armed*:
// a revision that has been reverted is spent and must never be offered again.
func TestSkillRevisions_LatestSkipsDisposed(t *testing.T) {
	store := revisionStore(t)
	ctx := context.Background()

	first := appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t1", Op: SkillOpCreate, SkillName: "greet", Actor: SkillActorSelf})
	second := appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t2", Op: SkillOpUpdate, SkillName: "greet", PriorPayload: strPtr("x"), Actor: SkillActorSelf})

	latest, err := store.LatestSkillRevision(ctx, "a", "greet")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest == nil || latest.ID != second {
		t.Fatalf("latest = %+v, want revision %d", latest, second)
	}

	if _, err := store.MarkSkillRevisionReverted(ctx, second); err != nil {
		t.Fatalf("marking reverted: %v", err)
	}
	latest, err = store.LatestSkillRevision(ctx, "a", "greet")
	if err != nil {
		t.Fatalf("latest after revert: %v", err)
	}
	if latest == nil || latest.ID != first {
		t.Fatalf("latest = %+v, want revision %d", latest, first)
	}

	if _, err := store.MarkSkillRevisionReverted(ctx, first); err != nil {
		t.Fatalf("marking reverted: %v", err)
	}
	latest, err = store.LatestSkillRevision(ctx, "a", "greet")
	if err != nil {
		t.Fatalf("latest when exhausted: %v", err)
	}
	if latest != nil {
		t.Errorf("expected nil when nothing is armed, got %+v", latest)
	}
}

// TestSkillRevisions_LatestScopedByAgentAndSkill proves an empty skill name
// means "anything this agent did", and that another agent's history is never
// visible.
func TestSkillRevisions_LatestScopedByAgentAndSkill(t *testing.T) {
	store := revisionStore(t)
	ctx := context.Background()

	appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t1", Op: SkillOpCreate, SkillName: "first", Actor: SkillActorSelf})
	newest := appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t2", Op: SkillOpCreate, SkillName: "second", Actor: SkillActorSelf})
	appendRev(t, store, SkillRevision{Agent: "b", TransitionID: "t3", Op: SkillOpCreate, SkillName: "third", Actor: SkillActorSelf})

	agentWide, err := store.LatestSkillRevision(ctx, "a", "")
	if err != nil {
		t.Fatalf("agent-wide latest: %v", err)
	}
	if agentWide == nil || agentWide.ID != newest {
		t.Errorf("agent-wide latest = %+v, want revision %d", agentWide, newest)
	}

	perSkill, err := store.LatestSkillRevision(ctx, "a", "first")
	if err != nil {
		t.Fatalf("per-skill latest: %v", err)
	}
	if perSkill == nil || perSkill.SkillName != "first" {
		t.Errorf("per-skill latest = %+v, want the 'first' revision", perSkill)
	}

	other, err := store.LatestSkillRevision(ctx, "c", "")
	if err != nil {
		t.Fatalf("unknown agent: %v", err)
	}
	if other != nil {
		t.Errorf("an agent with no history must get nil, got %+v", other)
	}
}

// TestSkillRevisions_MarkRevertedIsAtMostOnce is the whole safety mechanism:
// the conditional UPDATE, not a lock or a flag in memory, is what makes an undo
// unrepeatable — and it stays true across a restart.
func TestSkillRevisions_MarkRevertedIsAtMostOnce(t *testing.T) {
	store := revisionStore(t)
	ctx := context.Background()

	id := appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t1", Op: SkillOpUpdate, SkillName: "greet", PriorPayload: strPtr("x"), Actor: SkillActorSelf})

	claimed, err := store.MarkSkillRevisionReverted(ctx, id)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claimed {
		t.Fatal("first claim should have won")
	}

	claimed, err = store.MarkSkillRevisionReverted(ctx, id)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimed {
		t.Error("second claim must lose: a revision is spent exactly once")
	}

	rev, err := store.GetSkillRevision(ctx, id)
	if err != nil {
		t.Fatalf("re-reading revision: %v", err)
	}
	if rev.RevertedAt == nil {
		t.Error("reverted_at was not stamped")
	}
}

// TestSkillRevisions_MarkRevertedUnknownID reports "not claimed" rather than an
// error, so a caller races safely against a pruned row.
func TestSkillRevisions_MarkRevertedUnknownID(t *testing.T) {
	store := revisionStore(t)

	claimed, err := store.MarkSkillRevisionReverted(context.Background(), 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claimed {
		t.Error("claiming a revision that does not exist must not succeed")
	}
}

// TestSkillRevisions_TransitionListsArmedOnly keeps a partially-reverted
// transition from replaying the parts already undone.
func TestSkillRevisions_TransitionListsArmedOnly(t *testing.T) {
	store := revisionStore(t)
	ctx := context.Background()

	first := appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t1", Op: SkillOpCreate, SkillName: "one", Actor: SkillActorSelf})
	appendRev(t, store, SkillRevision{Agent: "a", TransitionID: "t1", Op: SkillOpUpdate, SkillName: "one", PriorPayload: strPtr("x"), Actor: SkillActorSelf})

	if _, err := store.MarkSkillRevisionReverted(ctx, first); err != nil {
		t.Fatalf("marking reverted: %v", err)
	}

	revs, err := store.TransitionSkillRevisions(ctx, "a", "t1")
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("got %d revisions, want only the armed one", len(revs))
	}
	if revs[0].Op != SkillOpUpdate {
		t.Errorf("remaining revision op = %q, want %q", revs[0].Op, SkillOpUpdate)
	}
}
