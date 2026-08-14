package skilleffect

import (
	"context"

	"github.com/Temikus/denkeeper/internal/agent"
)

// Binding is an Effector with the engine, actor and size cap already fixed, so
// a call site reads like the plain Apply* call it replaces. It mints a fresh
// transition id per call.
//
// Threading a per-turn transition id down from the engine's tool loop — so that
// several skill edits in one turn share a transition and revert together — is a
// follow-up; today every mutation is its own single-entry transition.
//
// *Binding satisfies configmcp.SkillWriter.
type Binding struct {
	eff      *Effector
	sa       SkillAccess
	actor    string
	maxBytes int
}

// Bind fixes the per-surface arguments. actor is one of the agent.SkillActor*
// constants: which surface asked for the change.
func (e *Effector) Bind(sa SkillAccess, actor string, maxBytes int) *Binding {
	return &Binding{eff: e, sa: sa, actor: actor, maxBytes: maxBytes}
}

func (b *Binding) Create(ctx context.Context, payload string) error {
	return b.eff.Create(ctx, b.sa, newTransitionID(), payload, b.actor, b.maxBytes)
}

func (b *Binding) Update(ctx context.Context, name, payload string) error {
	return b.eff.Update(ctx, b.sa, newTransitionID(), name, payload, b.actor, b.maxBytes)
}

func (b *Binding) Rename(ctx context.Context, oldName, payload string) error {
	return b.eff.Rename(ctx, b.sa, newTransitionID(), oldName, payload, b.actor, b.maxBytes)
}

func (b *Binding) Delete(ctx context.Context, name string) error {
	return b.eff.Delete(ctx, b.sa, newTransitionID(), name, b.actor, b.maxBytes)
}

// Revert undoes the newest armed revision for a skill, or for the whole agent
// when name is empty. The inverse is journaled with actor "revert" regardless
// of the binding's own actor.
func (b *Binding) Revert(ctx context.Context, name string) (*agent.SkillRevision, error) {
	return b.eff.Revert(ctx, b.sa, name, b.maxBytes)
}

// RevertTransition undoes a whole transition, newest revision first.
func (b *Binding) RevertTransition(ctx context.Context, tid string) ([]agent.SkillRevision, error) {
	return b.eff.RevertTransition(ctx, b.sa, tid, b.maxBytes)
}
