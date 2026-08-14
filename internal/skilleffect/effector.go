// Package skilleffect makes skill mutations revertible.
//
// It is a thin layer *above* the skill write helpers in internal/configmcp,
// which are unchanged: the same os.Root-confined, atomic writes do the work.
// What this package adds is a journal entry, written before each mutation, that
// captures the on-disk state the mutation is about to replace — and an inverse
// that replays that state at most once.
//
// Layering: agent ← configmcp ← skilleffect. This package may import both;
// neither may import it (configmcp receives an implementation of its own
// SkillWriter interface instead).
//
// Loop guard for a future auto-revert curator: a revision whose actor is
// SkillActorRevert records an undo, not a mistake. Nothing enforces this today,
// but an automated reverter that undoes such a revision would oscillate — it
// must skip them.
//
// Inspired by "A Programming Paradigm for Spatiotemporal Composability" (Shi,
// Zhang & Cui): https://github.com/cordiverse/paper/blob/main/paper.pdf
package skilleffect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skill"
)

// Compile-time interface checks — these are what keep the three declarations
// below honest against the types they were extracted from.
var (
	_ Store                 = (*agent.SQLiteMemoryStore)(nil)
	_ SkillAccess           = (*agent.Engine)(nil)
	_ configmcp.SkillWriter = (*Binding)(nil)
)

// Sentinel errors callers classify on.
var (
	// ErrJournalDisabled means no revision store is wired, so there is no
	// history to revert. Mutations still work — they are simply untracked.
	ErrJournalDisabled = errors.New("skill revision journal is not enabled")
	// ErrNoRevision means nothing armed was found to revert.
	ErrNoRevision = errors.New("no revertible skill revision found")
	// ErrAlreadyReverted means another caller (or an earlier call) claimed the
	// revision first. The claim is persisted, so this survives a restart.
	ErrAlreadyReverted = errors.New("skill revision has already been reverted")
)

// Store is the slice of the memory store the journal needs. It is satisfied by
// *agent.SQLiteMemoryStore.
type Store interface {
	AppendSkillRevision(ctx context.Context, r agent.SkillRevision) (int64, error)
	LatestSkillRevision(ctx context.Context, agentName, skillName string) (*agent.SkillRevision, error)
	GetSkillRevision(ctx context.Context, id int64) (*agent.SkillRevision, error)
	MarkSkillRevisionReverted(ctx context.Context, id int64) (bool, error)
	TransitionSkillRevisions(ctx context.Context, agentName, transitionID string) ([]agent.SkillRevision, error)
}

// SkillAccess is the slice of engine surface the effector needs. It is
// satisfied as-is by *agent.Engine.
type SkillAccess interface {
	SkillsDir() string
	GetSkill(name string) (skill.Skill, bool)
	AppendSkill(s skill.Skill)
	UpdateSkill(name string, s skill.Skill) bool
	RemoveSkill(name string) bool
}

// Effector performs tracked skill mutations for one agent.
//
// A nil store is a supported configuration: every mutation then degrades to the
// plain configmcp.Apply* call it wraps, so a deployment without a
// journal-capable store behaves exactly as it did before. Only Revert and
// RevertTransition require the journal.
type Effector struct {
	store  Store
	agent  string
	logger *slog.Logger
}

// New returns an Effector for one agent. store may be nil (journaling off).
func New(store Store, agentName string, logger *slog.Logger) *Effector {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Effector{store: store, agent: agentName, logger: logger}
}

// tracking reports whether mutations are journaled.
func (e *Effector) tracking() bool { return e != nil && e.store != nil }

// Create writes a new skill and journals that it did not exist before.
//
// Tracked-write ordering, shared by all four mutators: read the current state,
// append the revision, *then* perform the mutation. Journal-before-write means
// a crash between the two leaves a revision describing a mutation that never
// happened — reverting it restores the payload already on disk, an idempotent
// no-op. Write-before-journal would leave an untracked mutation, which is the
// one failure mode this design exists to prevent. A failed append therefore
// aborts the mutation: fail closed.
func (e *Effector) Create(ctx context.Context, sa SkillAccess, tid, payload, actor string, maxBytes int) error {
	if name, version, ok := e.parseTracked(payload); ok && validName(name) {
		// prior_payload stays NULL: the prior state of a create is "absent",
		// which is what tells the reverter to delete rather than restore.
		if err := e.journal(ctx, agent.SkillRevision{
			TransitionID: tid,
			Op:           agent.SkillOpCreate,
			SkillName:    name,
			NewVersion:   version,
			Actor:        actor,
		}); err != nil {
			return err
		}
	}
	return configmcp.ApplySkillCreate(sa.SkillsDir(), sa.AppendSkill, e.logger, payload, maxBytes)
}

// Update replaces a skill's file, journaling the bytes it replaces.
func (e *Effector) Update(ctx context.Context, sa SkillAccess, tid, name, payload, actor string, maxBytes int) error {
	if _, version, ok := e.parseTracked(payload); ok && validName(name) {
		prior, err := e.readPrior(sa, name)
		if err != nil {
			return err
		}
		if err := e.journal(ctx, agent.SkillRevision{
			TransitionID: tid,
			Op:           agent.SkillOpUpdate,
			SkillName:    name,
			PriorPayload: prior,
			NewVersion:   version,
			PriorVersion: versionOf(prior),
			Actor:        actor,
		}); err != nil {
			return err
		}
	}
	return configmcp.ApplySkillUpdate(sa.SkillsDir(), sa.UpdateSkill, e.logger, name, payload, maxBytes)
}

// Rename writes payload under the name its frontmatter declares and removes
// oldName, journaling both the old name and the bytes it held.
func (e *Effector) Rename(ctx context.Context, sa SkillAccess, tid, oldName, payload, actor string, maxBytes int) error {
	if newName, version, ok := e.parseTracked(payload); ok && validName(newName) && validName(oldName) {
		prior, err := e.readPrior(sa, oldName)
		if err != nil {
			return err
		}
		from := oldName
		if err := e.journal(ctx, agent.SkillRevision{
			TransitionID: tid,
			Op:           agent.SkillOpRename,
			SkillName:    newName,
			PriorName:    &from,
			PriorPayload: prior,
			NewVersion:   version,
			PriorVersion: versionOf(prior),
			Actor:        actor,
		}); err != nil {
			return err
		}
	}
	return configmcp.ApplySkillRename(sa.SkillsDir(), sa.RemoveSkill, sa.AppendSkill, e.logger, oldName, payload, maxBytes)
}

// Delete removes a skill, journaling the full bytes it held so the file can be
// restored verbatim.
//
// maxBytes is accepted for symmetry with the other mutators (and so a caller
// can bind one writer for all four); a removal writes nothing, so it is unused.
func (e *Effector) Delete(ctx context.Context, sa SkillAccess, tid, name, actor string, _ int) error {
	if e.tracking() && validName(name) {
		prior, err := e.readPrior(sa, name)
		if err != nil {
			return err
		}
		if err := e.journal(ctx, agent.SkillRevision{
			TransitionID: tid,
			Op:           agent.SkillOpDelete,
			SkillName:    name,
			PriorPayload: prior,
			PriorVersion: versionOf(prior),
			Actor:        actor,
		}); err != nil {
			return err
		}
	}

	// Disk-first: remove the file before mutating memory, so a real IO error
	// leaves the skill intact in memory and on the next reload.
	if err := configmcp.RemoveSkillFile(sa.SkillsDir(), name); err != nil {
		return err
	}
	if !sa.RemoveSkill(name) {
		e.logger.Info("skill file removed; skill was not present in memory", "name", name)
	}
	return nil
}

// Revert undoes the newest armed revision for a skill, or for the whole agent
// when name is empty, and returns the revision it undid.
func (e *Effector) Revert(ctx context.Context, sa SkillAccess, name string, maxBytes int) (*agent.SkillRevision, error) {
	if !e.tracking() {
		return nil, ErrJournalDisabled
	}
	rev, err := e.store.LatestSkillRevision(ctx, e.agent, name)
	if err != nil {
		return nil, err
	}
	if rev == nil {
		return nil, ErrNoRevision
	}
	if err := e.revertOne(ctx, sa, *rev, newTransitionID(), maxBytes); err != nil {
		return nil, err
	}
	return rev, nil
}

// RevertByID undoes one specific revision and returns it.
func (e *Effector) RevertByID(ctx context.Context, sa SkillAccess, id int64, maxBytes int) (*agent.SkillRevision, error) {
	if !e.tracking() {
		return nil, ErrJournalDisabled
	}
	rev, err := e.store.GetSkillRevision(ctx, id)
	if err != nil {
		return nil, err
	}
	if rev == nil {
		return nil, ErrNoRevision
	}
	if err := e.revertOne(ctx, sa, *rev, newTransitionID(), maxBytes); err != nil {
		return nil, err
	}
	return rev, nil
}

// RevertTransition undoes every armed revision of one transition, newest first.
// LIFO is required, not cosmetic: a rename followed by an update must be undone
// update-then-rename, or the update targets a name that no longer exists.
//
// It stops at the first failure and reports how far it got; the revisions it
// did undo stay undone (each is claimed and applied individually).
func (e *Effector) RevertTransition(ctx context.Context, sa SkillAccess, tid string, maxBytes int) ([]agent.SkillRevision, error) {
	if !e.tracking() {
		return nil, ErrJournalDisabled
	}
	revs, err := e.store.TransitionSkillRevisions(ctx, e.agent, tid)
	if err != nil {
		return nil, err
	}
	if len(revs) == 0 {
		return nil, ErrNoRevision
	}

	// One fresh transition groups all the inverses, so undoing the undo is
	// itself a single transition rather than N unrelated ones.
	inverseTID := newTransitionID()
	done := make([]agent.SkillRevision, 0, len(revs))
	for _, rev := range revs {
		if err := e.revertOne(ctx, sa, rev, inverseTID, maxBytes); err != nil {
			return done, fmt.Errorf("partially reverted at seq %d: %w", rev.Seq, err)
		}
		done = append(done, rev)
	}
	return done, nil
}

// revertOne claims a revision, then applies its inverse.
//
// Claim-then-revert, not revert-then-claim: a crash between the two loses one
// undo, whereas the other order risks applying the same inverse twice.
func (e *Effector) revertOne(ctx context.Context, sa SkillAccess, rev agent.SkillRevision, tid string, maxBytes int) error {
	claimed, err := e.store.MarkSkillRevisionReverted(ctx, rev.ID)
	if err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("%w: revision %d", ErrAlreadyReverted, rev.ID)
	}
	return e.applyInverse(ctx, sa, rev, tid, maxBytes)
}

// applyInverse performs the mutation that undoes rev.
//
// Every branch goes back through the tracked mutators with actor "revert", so
// the undo is journaled like any other change and "undo the undo" comes free.
// A nil PriorPayload always means "the file did not exist", so the inverse is a
// removal regardless of which op recorded it.
func (e *Effector) applyInverse(ctx context.Context, sa SkillAccess, rev agent.SkillRevision, tid string, maxBytes int) error {
	switch rev.Op {
	case agent.SkillOpCreate:
		return e.Delete(ctx, sa, tid, rev.SkillName, agent.SkillActorRevert, maxBytes)

	case agent.SkillOpUpdate:
		if rev.PriorPayload == nil {
			return e.Delete(ctx, sa, tid, rev.SkillName, agent.SkillActorRevert, maxBytes)
		}
		return e.Update(ctx, sa, tid, rev.SkillName, priorAsPayload(rev), agent.SkillActorRevert, maxBytes)

	case agent.SkillOpDelete:
		if rev.PriorPayload == nil {
			// Nothing was on disk when the delete ran, so there is nothing to
			// put back. The claim above still consumed the revision.
			return nil
		}
		return e.Create(ctx, sa, tid, priorAsPayload(rev), agent.SkillActorRevert, maxBytes)

	case agent.SkillOpRename:
		if rev.PriorPayload == nil {
			return e.Delete(ctx, sa, tid, rev.SkillName, agent.SkillActorRevert, maxBytes)
		}
		// Rename back: the prior bytes carry the old name in their own
		// frontmatter, which is the name they are restored under.
		return e.Rename(ctx, sa, tid, rev.SkillName, priorAsPayload(rev), agent.SkillActorRevert, maxBytes)

	default:
		return fmt.Errorf("unknown skill revision op %q on revision %d", rev.Op, rev.ID)
	}
}

// journal stamps the agent name and appends the revision. A failure here aborts
// the caller's mutation — see Create's comment on fail-closed ordering.
func (e *Effector) journal(ctx context.Context, rev agent.SkillRevision) error {
	rev.Agent = e.agent
	id, err := e.store.AppendSkillRevision(ctx, rev)
	if err != nil {
		return fmt.Errorf("journaling skill %s: %w", rev.Op, err)
	}
	e.logger.Debug("skill revision journaled",
		"id", id, "agent", e.agent, "op", rev.Op, "skill", rev.SkillName,
		"transition", rev.TransitionID, "actor", rev.Actor)
	return nil
}

// readPrior returns the raw bytes currently on disk for a skill, or nil when no
// file exists.
//
// An unreadable file is an error, not an absent one: journaling NULL for a file
// that actually has content would make the revert delete it.
func (e *Effector) readPrior(sa SkillAccess, name string) (*string, error) {
	payload, ok, err := configmcp.ReadSkillFile(sa.SkillsDir(), name)
	if err != nil {
		return nil, fmt.Errorf("reading prior state of skill %q: %w", name, err)
	}
	if !ok {
		return nil, nil
	}
	return &payload, nil
}

// parseTracked answers "should this mutation be journaled, and under what name
// and version?". ok is false when journaling is off, or when the payload does
// not parse — in the latter case the write helper below rejects it with its own
// error, so a bad payload never produces a revision naming a skill that does
// not exist.
func (e *Effector) parseTracked(payload string) (name, version string, ok bool) {
	if !e.tracking() {
		return "", "", false
	}
	s, err := skill.ParseFile("(runtime)", []byte(payload))
	if err != nil {
		return "", "", false
	}
	return s.Name, s.Version, true
}

// priorAsPayload turns journaled file bytes back into a payload. The writer
// appends a trailing newline, so handing a file's own bytes straight back would
// grow it by one byte per undo — the restore would not be byte-identical, which
// is the whole promise. Callers must have checked PriorPayload is non-nil.
func priorAsPayload(rev agent.SkillRevision) string {
	return configmcp.SkillPayloadFromFile(*rev.PriorPayload)
}

// validName reports whether the write helpers would accept this name. A name
// they will reject must not be journaled: the revision would name a skill that
// can never exist, and its inverse could never run.
func validName(name string) bool {
	return configmcp.ValidateSkillName(name) == nil
}

// versionOf reads the frontmatter version out of prior on-disk bytes. Prior
// bytes that do not parse journal an empty version rather than blocking the
// mutation: the version is a telemetry join key, not a correctness input.
func versionOf(payload *string) string {
	if payload == nil {
		return ""
	}
	s, err := skill.ParseFile("(prior)", []byte(*payload))
	if err != nil {
		return ""
	}
	return s.Version
}

// newTransitionID mints the id that groups one edit's revisions.
func newTransitionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; a fixed id would still be
		// grouped correctly per call site, just not unique across calls.
		return "transition-unknown"
	}
	return hex.EncodeToString(b[:])
}
