package agent

const memoryReviewPrompt = `Review the conversation so far and determine if MEMORY.md should be updated.

Consider:
- Are there facts, preferences, or decisions worth remembering long-term?
- Is existing memory still accurate or should entries be removed/updated?
- Is memory approaching capacity? If so, consolidate or remove stale entries.

Use persona_memory_manage to append, remove, or replace memory entries as needed.
If no changes are warranted, respond briefly that memory is up to date.`

const skillReviewPrompt = `Review recent tool usage and determine if any skills should be created or updated.

Consider:
- Were there repeated multi-step workflows that could become a skill?
- Did an existing skill produce suboptimal results that suggest a body update?
- Are there skills that haven't been used recently and may be stale?

Use skill_create, skill_update, or skill_patch as needed.
If no changes are warranted, respond briefly that skills are up to date.`

func buildReviewPrompt(reviewMemory, reviewSkills bool) string {
	switch {
	case reviewMemory && reviewSkills:
		return memoryReviewPrompt + "\n\n---\n\n" + skillReviewPrompt
	case reviewMemory:
		return memoryReviewPrompt
	case reviewSkills:
		return skillReviewPrompt
	default:
		return ""
	}
}
