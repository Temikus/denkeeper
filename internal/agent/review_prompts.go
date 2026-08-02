package agent

// The reviewer engine runs unattended with a deliberately reduced tool set
// (see reviewerConfigDeps in cmd/denkeeper/main.go): read-only skill access
// and append-only memory. These prompts must not instruct it to call tools it
// does not have, or it burns rounds discovering their absence. Skill changes
// are reported as text for skill-forge's weekly pass; the propose-only KV
// handoff in design/heartbeat-improvements-2026-07.md Step 3.6 is a follow-up,
// not an oversight.

const memoryReviewPrompt = `Review the conversation so far and determine if MEMORY.md should be updated.

Consider:
- Are there facts, preferences, or decisions worth remembering long-term?
- Is existing memory still accurate, or has some of it gone stale?
- Is memory approaching capacity?

Use persona_memory_manage with operation "append" to add an entry. You cannot
remove, replace, or consolidate memory — if entries are stale or memory is near
capacity, say so in your response and the gardener will prune it.
If no changes are warranted, respond briefly that memory is up to date.`

const skillReviewPrompt = `Review recent tool usage and determine if any skills should be created or updated.

Consider:
- Were there repeated multi-step workflows that could become a skill?
- Did an existing skill produce suboptimal results that suggest a body update?
- Are there skills that haven't been used recently and may be stale?

You have read-only access to skills (skill_list, skill_get, skill_read_file).
You cannot create, update, or patch them. If you find a change worth making,
state the skill name, the specific edit, and why — skill-forge's weekly pass
acts on these reports.
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
