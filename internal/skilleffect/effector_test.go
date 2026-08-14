package skilleffect_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/skilleffect"
)

const testAgent = "test-agent"

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeSkills is the in-memory half of SkillAccess: a skills directory plus the
// registry an Engine would keep.
type fakeSkills struct {
	dir    string
	skills map[string]skill.Skill
}

func newFakeSkills(t *testing.T) *fakeSkills {
	t.Helper()
	return &fakeSkills{
		dir:    filepath.Join(t.TempDir(), "skills"),
		skills: map[string]skill.Skill{},
	}
}

func (f *fakeSkills) SkillsDir() string { return f.dir }

func (f *fakeSkills) GetSkill(name string) (skill.Skill, bool) {
	s, ok := f.skills[name]
	return s, ok
}

func (f *fakeSkills) AppendSkill(s skill.Skill) { f.skills[s.Name] = s }

func (f *fakeSkills) UpdateSkill(name string, s skill.Skill) bool {
	if _, ok := f.skills[name]; !ok {
		return false
	}
	delete(f.skills, name)
	f.skills[s.Name] = s
	return true
}

func (f *fakeSkills) RemoveSkill(name string) bool {
	if _, ok := f.skills[name]; !ok {
		return false
	}
	delete(f.skills, name)
	return true
}

// newStore returns a real SQLite store, so the tests exercise the actual
// conditional-UPDATE claim rather than a mock of it.
func newStore(t *testing.T) *agent.SQLiteMemoryStore {
	t.Helper()
	store, err := agent.NewInMemoryStore()
	if err != nil {
		t.Fatalf("opening in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// newSharedStore returns a file-backed store. Concurrent tests need one:
// ":memory:" gives every pooled connection its own empty database, so a second
// goroutine would find no tables at all.
func newSharedStore(t *testing.T) *agent.SQLiteMemoryStore {
	t.Helper()
	store, err := agent.NewSQLiteMemoryStore(filepath.Join(t.TempDir(), "revisions.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newEffector(t *testing.T, store skilleffect.Store) *skilleffect.Effector {
	t.Helper()
	return skilleffect.New(store, testAgent, testLogger())
}

func payloadFor(name, version, body string) string {
	return configmcp.BuildSkillPayload(name, "desc", version, nil, body, 0, nil)
}

// onDisk returns the raw bytes of a skill file, failing the test if it is
// missing. Comparisons use these bytes verbatim: the writer appends a trailing
// newline, and a faithful revert has to reproduce that too.
func onDisk(t *testing.T, sa *fakeSkills, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sa.dir, name+".md"))
	if err != nil {
		t.Fatalf("reading %s.md: %v", name, err)
	}
	return string(data)
}

func mustNotExist(t *testing.T, sa *fakeSkills, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(sa.dir, name+".md")); !os.IsNotExist(err) {
		t.Errorf("expected %s.md to be gone, stat err = %v", name, err)
	}
}

// seed creates a skill through the tracked path and returns its on-disk bytes.
func seed(t *testing.T, e *skilleffect.Effector, sa *fakeSkills, name, version, body string) string {
	t.Helper()
	if err := e.Create(context.Background(), sa, "seed-"+name, payloadFor(name, version, body), agent.SkillActorUser, 0); err != nil {
		t.Fatalf("seeding skill %q: %v", name, err)
	}
	return onDisk(t, sa, name)
}

func latest(t *testing.T, store *agent.SQLiteMemoryStore, skillName string) *agent.SkillRevision {
	t.Helper()
	rev, err := store.LatestSkillRevision(context.Background(), testAgent, skillName)
	if err != nil {
		t.Fatalf("reading latest revision: %v", err)
	}
	return rev
}

// TestEffector_Update_JournalsPriorBytesBeforeWrite is the core invariant: the
// revision holds the exact bytes the file had before the update, both versions
// for the telemetry join, and the file holds the new payload afterwards.
func TestEffector_Update_JournalsPriorBytesBeforeWrite(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	before := seed(t, e, sa, "greet", "1.0.0", "old body")

	newPayload := payloadFor("greet", "2.0.0", "new body")
	if err := e.Update(context.Background(), sa, "t-update", "greet", newPayload, agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("update: %v", err)
	}

	rev := latest(t, store, "greet")
	if rev == nil {
		t.Fatal("no revision journaled for the update")
	}
	if rev.Op != agent.SkillOpUpdate {
		t.Errorf("op = %q, want %q", rev.Op, agent.SkillOpUpdate)
	}
	if rev.Actor != agent.SkillActorSelf {
		t.Errorf("actor = %q, want %q", rev.Actor, agent.SkillActorSelf)
	}
	if rev.PriorPayload == nil {
		t.Fatal("prior payload not journaled")
	}
	if *rev.PriorPayload != before {
		t.Errorf("prior payload is not the exact prior bytes:\n got %q\nwant %q", *rev.PriorPayload, before)
	}
	if rev.PriorVersion != "1.0.0" || rev.NewVersion != "2.0.0" {
		t.Errorf("versions = (prior %q, new %q), want (1.0.0, 2.0.0)", rev.PriorVersion, rev.NewVersion)
	}
	if rev.RevertedAt != nil {
		t.Error("a fresh revision must be armed (reverted_at NULL)")
	}

	if got := onDisk(t, sa, "greet"); got != newPayload+"\n" {
		t.Errorf("file does not hold the new payload:\n got %q\nwant %q", got, newPayload+"\n")
	}
}

// TestEffector_Create_JournalsAbsentPriorState proves a create records "nothing
// was here", which is what tells the reverter to delete rather than restore.
func TestEffector_Create_JournalsAbsentPriorState(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	seed(t, e, sa, "greet", "1.0.0", "body")

	rev := latest(t, store, "greet")
	if rev == nil {
		t.Fatal("no revision journaled for the create")
	}
	if rev.Op != agent.SkillOpCreate {
		t.Errorf("op = %q, want %q", rev.Op, agent.SkillOpCreate)
	}
	if rev.PriorPayload != nil {
		t.Errorf("create must journal a NULL prior payload, got %q", *rev.PriorPayload)
	}
	if rev.NewVersion != "1.0.0" {
		t.Errorf("new version = %q, want 1.0.0", rev.NewVersion)
	}
}

// failingStore fails every append, leaving everything else to the real store.
type failingStore struct {
	skilleffect.Store
	err error
}

func (f failingStore) AppendSkillRevision(context.Context, agent.SkillRevision) (int64, error) {
	return 0, f.err
}

// TestEffector_JournalFailureAbortsMutation proves the ordering is fail-closed:
// if the mutation cannot be journaled it does not happen, because an untracked
// mutation is the one failure mode the journal exists to prevent.
func TestEffector_JournalFailureAbortsMutation(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)

	before := seed(t, newEffector(t, store), sa, "greet", "1.0.0", "old body")

	broken := newEffector(t, failingStore{Store: store, err: errors.New("disk on fire")})
	err := broken.Update(context.Background(), sa, "t-fail", "greet", payloadFor("greet", "2.0.0", "new body"), agent.SkillActorUser, 0)
	if err == nil {
		t.Fatal("expected the update to fail when the journal append fails")
	}

	if got := onDisk(t, sa, "greet"); got != before {
		t.Errorf("file was mutated despite the journal failure:\n got %q\nwant %q", got, before)
	}
	if sk, ok := sa.GetSkill("greet"); !ok || sk.Body != "old body" {
		t.Errorf("in-memory skill was mutated despite the journal failure: %+v", sk)
	}
}

// TestEffector_Revert_RestoresPriorBytesByteIdentical is the user-visible
// promise: after a revert the file is what it was, byte for byte.
func TestEffector_Revert_RestoresPriorBytesByteIdentical(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	before := seed(t, e, sa, "greet", "1.0.0", "old body")
	if err := e.Update(context.Background(), sa, "t1", "greet", payloadFor("greet", "2.0.0", "new body"), agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("update: %v", err)
	}

	rev, err := e.Revert(context.Background(), sa, "greet", 0)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if rev.Op != agent.SkillOpUpdate {
		t.Errorf("reverted op = %q, want %q", rev.Op, agent.SkillOpUpdate)
	}

	if got := onDisk(t, sa, "greet"); got != before {
		t.Errorf("revert did not restore the exact prior bytes:\n got %q\nwant %q", got, before)
	}
	if sk, ok := sa.GetSkill("greet"); !ok || sk.Version != "1.0.0" || sk.Body != "old body" {
		t.Errorf("in-memory skill not restored: %+v", sk)
	}
}

// TestEffector_Revert_DeletedSkillIsRecreated covers the delete inverse.
func TestEffector_Revert_DeletedSkillIsRecreated(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	before := seed(t, e, sa, "greet", "1.0.0", "body")
	if err := e.Delete(context.Background(), sa, "t1", "greet", agent.SkillActorUser, 0); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustNotExist(t, sa, "greet")

	if _, err := e.Revert(context.Background(), sa, "greet", 0); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got := onDisk(t, sa, "greet"); got != before {
		t.Errorf("deleted skill not restored byte-identically:\n got %q\nwant %q", got, before)
	}
	if _, ok := sa.GetSkill("greet"); !ok {
		t.Error("restored skill missing from memory")
	}
}

// TestEffector_Revert_CreatedSkillIsRemoved covers the create inverse.
func TestEffector_Revert_CreatedSkillIsRemoved(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	seed(t, e, sa, "greet", "1.0.0", "body")

	if _, err := e.Revert(context.Background(), sa, "greet", 0); err != nil {
		t.Fatalf("revert: %v", err)
	}
	mustNotExist(t, sa, "greet")
	if _, ok := sa.GetSkill("greet"); ok {
		t.Error("reverted create left the skill in memory")
	}
}

// TestEffector_Revert_AlreadyReverted proves a revision is spent exactly once.
func TestEffector_Revert_AlreadyReverted(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	seed(t, e, sa, "greet", "1.0.0", "old body")
	if err := e.Update(context.Background(), sa, "t1", "greet", payloadFor("greet", "2.0.0", "new body"), agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("update: %v", err)
	}
	target := latest(t, store, "greet")

	if _, err := e.RevertByID(context.Background(), sa, target.ID, 0); err != nil {
		t.Fatalf("first revert: %v", err)
	}
	_, err := e.RevertByID(context.Background(), sa, target.ID, 0)
	if !errors.Is(err, skilleffect.ErrAlreadyReverted) {
		t.Fatalf("second revert error = %v, want ErrAlreadyReverted", err)
	}
}

// countingStore counts how many revisions were appended with a given actor, so
// a double-apply shows up as a second journal entry rather than as a silently
// identical file.
type countingStore struct {
	skilleffect.Store
	real    *agent.SQLiteMemoryStore
	mu      sync.Mutex
	byActor map[string]int
}

func (c *countingStore) AppendSkillRevision(ctx context.Context, r agent.SkillRevision) (int64, error) {
	c.mu.Lock()
	c.byActor[r.Actor]++
	c.mu.Unlock()
	return c.Store.AppendSkillRevision(ctx, r)
}

func (c *countingStore) count(actor string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byActor[actor]
}

// TestEffector_Revert_ConcurrentClaimAppliesOnce runs two reverts of the same
// revision at once: exactly one may claim it, and the inverse must run exactly
// once. Run with -race.
func TestEffector_Revert_ConcurrentClaimAppliesOnce(t *testing.T) {
	real := newSharedStore(t)
	counting := &countingStore{Store: real, real: real, byActor: map[string]int{}}
	sa := newFakeSkills(t)
	e := newEffector(t, counting)

	before := seed(t, e, sa, "greet", "1.0.0", "old body")
	if err := e.Update(context.Background(), sa, "t1", "greet", payloadFor("greet", "2.0.0", "new body"), agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("update: %v", err)
	}
	target := latest(t, counting.real, "greet")

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		lastErr error
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.RevertByID(context.Background(), sa, target.ID, 0)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else {
				lastErr = err
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Fatalf("%d concurrent reverts succeeded, want exactly 1 (last error: %v)", wins, lastErr)
	}
	if !errors.Is(lastErr, skilleffect.ErrAlreadyReverted) {
		t.Errorf("loser error = %v, want ErrAlreadyReverted", lastErr)
	}
	if n := counting.count(agent.SkillActorRevert); n != 1 {
		t.Errorf("%d revert revisions journaled, want 1 (the inverse ran twice)", n)
	}
	if got := onDisk(t, sa, "greet"); got != before {
		t.Errorf("file after concurrent revert:\n got %q\nwant %q", got, before)
	}
}

// TestEffector_RevertTransition_LIFO proves the ordering matters: a rename
// followed by an update must be undone update-then-rename, or the update would
// target a name that no longer exists.
func TestEffector_RevertTransition_LIFO(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	before := seed(t, e, sa, "old", "1.0.0", "original body")

	const tid = "one-edit"
	if err := e.Rename(context.Background(), sa, tid, "old", payloadFor("new", "2.0.0", "renamed body"), agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := e.Update(context.Background(), sa, tid, "new", payloadFor("new", "3.0.0", "edited body"), agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("update: %v", err)
	}

	reverted, err := e.RevertTransition(context.Background(), sa, tid, 0)
	if err != nil {
		t.Fatalf("revert transition: %v", err)
	}
	if len(reverted) != 2 {
		t.Fatalf("reverted %d revisions, want 2", len(reverted))
	}
	if reverted[0].Op != agent.SkillOpUpdate || reverted[1].Op != agent.SkillOpRename {
		t.Errorf("revert order = %q then %q, want update then rename", reverted[0].Op, reverted[1].Op)
	}

	if got := onDisk(t, sa, "old"); got != before {
		t.Errorf("original skill not restored:\n got %q\nwant %q", got, before)
	}
	mustNotExist(t, sa, "new")
	if _, ok := sa.GetSkill("new"); ok {
		t.Error("renamed skill still in memory after revert")
	}
	if _, ok := sa.GetSkill("old"); !ok {
		t.Error("original skill missing from memory after revert")
	}
}

// TestEffector_RevertTransition_CreateThenUpdateLeavesNoFile proves the
// transition unwinds all the way back to "the skill never existed".
func TestEffector_RevertTransition_CreateThenUpdateLeavesNoFile(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	const tid = "born-and-edited"
	if err := e.Create(context.Background(), sa, tid, payloadFor("greet", "1.0.0", "first body"), agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := e.Update(context.Background(), sa, tid, "greet", payloadFor("greet", "1.1.0", "second body"), agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := e.RevertTransition(context.Background(), sa, tid, 0); err != nil {
		t.Fatalf("revert transition: %v", err)
	}

	mustNotExist(t, sa, "greet")
	if _, ok := sa.GetSkill("greet"); ok {
		t.Error("skill still in memory after the whole transition was reverted")
	}
}

// TestEffector_Revert_IsJournaled proves a revert is itself a tracked mutation,
// so undoing the undo is free.
func TestEffector_Revert_IsJournaled(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	seed(t, e, sa, "greet", "1.0.0", "old body")
	updated := payloadFor("greet", "2.0.0", "new body")
	if err := e.Update(context.Background(), sa, "t1", "greet", updated, agent.SkillActorSelf, 0); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := e.Revert(context.Background(), sa, "greet", 0); err != nil {
		t.Fatalf("revert: %v", err)
	}

	rev := latest(t, store, "greet")
	if rev == nil {
		t.Fatal("the revert itself was not journaled")
	}
	if rev.Actor != agent.SkillActorRevert {
		t.Errorf("actor = %q, want %q", rev.Actor, agent.SkillActorRevert)
	}

	// Undo the undo: the newest armed revision is the revert, so reverting
	// again puts the update back.
	if _, err := e.Revert(context.Background(), sa, "greet", 0); err != nil {
		t.Fatalf("undo the undo: %v", err)
	}
	if got := onDisk(t, sa, "greet"); got != updated+"\n" {
		t.Errorf("undo-the-undo did not restore the update:\n got %q\nwant %q", got, updated+"\n")
	}
}

// TestEffector_NilStore_IsPlainPassthrough proves a deployment without a
// journal-capable store keeps working — writes happen, and only the revert
// entry points refuse.
func TestEffector_NilStore_IsPlainPassthrough(t *testing.T) {
	sa := newFakeSkills(t)
	e := skilleffect.New(nil, testAgent, testLogger())

	payload := payloadFor("greet", "1.0.0", "body")
	if err := e.Create(context.Background(), sa, "t1", payload, agent.SkillActorUser, 0); err != nil {
		t.Fatalf("create with no store: %v", err)
	}
	if got := onDisk(t, sa, "greet"); got != payload+"\n" {
		t.Errorf("untracked create did not write the payload: %q", got)
	}

	if _, err := e.Revert(context.Background(), sa, "greet", 0); !errors.Is(err, skilleffect.ErrJournalDisabled) {
		t.Errorf("revert error = %v, want ErrJournalDisabled", err)
	}
}

// TestEffector_Revert_NoRevision reports "nothing to undo" distinctly from a
// failure, so a caller can tell the two apart.
func TestEffector_Revert_NoRevision(t *testing.T) {
	sa := newFakeSkills(t)
	e := newEffector(t, newStore(t))

	if _, err := e.Revert(context.Background(), sa, "never-existed", 0); !errors.Is(err, skilleffect.ErrNoRevision) {
		t.Errorf("revert error = %v, want ErrNoRevision", err)
	}
	if _, err := e.RevertTransition(context.Background(), sa, "no-such-transition", 0); !errors.Is(err, skilleffect.ErrNoRevision) {
		t.Errorf("revert transition error = %v, want ErrNoRevision", err)
	}
}

// TestEffector_Revert_AgentWideWhenNameOmitted picks the newest change across
// every skill the agent owns.
func TestEffector_Revert_AgentWideWhenNameOmitted(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	seed(t, e, sa, "first", "1.0.0", "first body")
	seed(t, e, sa, "second", "1.0.0", "second body")

	rev, err := e.Revert(context.Background(), sa, "", 0)
	if err != nil {
		t.Fatalf("agent-wide revert: %v", err)
	}
	if rev.SkillName != "second" {
		t.Errorf("reverted %q, want the most recent change (second)", rev.SkillName)
	}
	mustNotExist(t, sa, "second")
	if _, err := os.Stat(filepath.Join(sa.dir, "first.md")); err != nil {
		t.Errorf("untouched skill was affected: %v", err)
	}
}

// TestEffector_UnparseablePayload_JournalsNothing proves a payload the write
// helper will reject never leaves a revision naming a skill that cannot exist.
func TestEffector_UnparseablePayload_JournalsNothing(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	if err := e.Create(context.Background(), sa, "t1", "no frontmatter here", agent.SkillActorUser, 0); err == nil {
		t.Fatal("expected an error for an unparseable payload")
	}
	if rev := latest(t, store, ""); rev != nil {
		t.Errorf("a rejected payload was journaled: %+v", rev)
	}
}

// TestEffector_TraversalName_JournalsNothing is the same guarantee for a name
// the write helpers refuse on path-safety grounds.
func TestEffector_TraversalName_JournalsNothing(t *testing.T) {
	store := newStore(t)
	sa := newFakeSkills(t)
	e := newEffector(t, store)

	if err := e.Create(context.Background(), sa, "t1", payloadFor("../escape", "1.0.0", "evil"), agent.SkillActorUser, 0); err == nil {
		t.Fatal("expected an error for a traversal name")
	}
	if rev := latest(t, store, ""); rev != nil {
		t.Errorf("a traversal name was journaled: %+v", rev)
	}
}
