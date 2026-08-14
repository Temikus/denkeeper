package configmcp

import "context"

// SkillWriter performs a *tracked* skill mutation: it records the prior on-disk
// state in an undo journal before performing the write, so the mutation can be
// reverted later. Its four methods mirror the Apply* helpers in handlers.go,
// with the skills directory, size cap and in-memory callbacks already bound.
//
// It is an interface injected through Deps rather than a direct dependency
// because the effect layer that implements it (internal/skilleffect) imports
// this package for those helpers — configmcp must not import it back, or the
// agent ← configmcp ← skilleffect layering becomes a cycle.
//
// A nil Deps.SkillWriter is not an error: skillWriter() falls back to an
// untracked passthrough, so a deployment with no journal-capable store keeps
// working exactly as it did before, just without an undo history.
type SkillWriter interface {
	// Create writes a new skill from payload (its name comes from the
	// frontmatter) and registers it in memory.
	Create(ctx context.Context, payload string) error
	// Update replaces the named skill's file and its in-memory entry.
	Update(ctx context.Context, name, payload string) error
	// Rename writes payload under the name its frontmatter declares, removes
	// oldName's file, and swaps the in-memory entry.
	Rename(ctx context.Context, oldName, payload string) error
	// Delete removes the named skill's file, then its in-memory entry.
	Delete(ctx context.Context, name string) error
}

// skillWriter returns the injected tracked writer, or an untracked passthrough
// to the Apply* helpers when none is wired.
func (d Deps) skillWriter() SkillWriter {
	if d.SkillWriter != nil {
		return d.SkillWriter
	}
	return directSkillWriter{deps: d}
}

// directSkillWriter is the untracked fallback — exactly the Apply* calls the
// handlers made before the journal existed, so behaviour with no writer wired
// is unchanged.
type directSkillWriter struct{ deps Deps }

func (w directSkillWriter) Create(_ context.Context, payload string) error {
	return ApplySkillCreate(w.deps.AgentSkillsDir, w.deps.AppendSkill, w.deps.Logger, payload, w.deps.MaxSkillBytes)
}

func (w directSkillWriter) Update(_ context.Context, name, payload string) error {
	return ApplySkillUpdate(w.deps.AgentSkillsDir, w.deps.UpdateSkill, w.deps.Logger, name, payload, w.deps.MaxSkillBytes)
}

func (w directSkillWriter) Rename(_ context.Context, oldName, payload string) error {
	return ApplySkillRename(w.deps.AgentSkillsDir, w.deps.RemoveSkill, w.deps.AppendSkill, w.deps.Logger, oldName, payload, w.deps.MaxSkillBytes)
}

func (w directSkillWriter) Delete(_ context.Context, name string) error {
	// Disk-first: remove the file before mutating memory, so a real IO error
	// leaves the skill intact in memory and on the next reload (matching
	// create/update/rename's persist-failure semantics).
	if err := RemoveSkillFile(w.deps.AgentSkillsDir, name); err != nil {
		return err
	}
	if w.deps.RemoveSkill != nil && !w.deps.RemoveSkill(name) {
		w.deps.Logger.Info("skill file removed; skill was not present in memory", "name", name)
	}
	return nil
}
