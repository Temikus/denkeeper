// Eval test-case categories, in the order internal/eval Categories() returns
// them. Stored as slugs; every view labels them from here so they agree.
export const CATEGORIES = [
  { value: 'chat', label: 'Chat / persona' },
  { value: 'skill_command', label: 'Skill command' },
  { value: 'scheduled', label: 'Scheduled' },
  { value: 'tool_heavy', label: 'Tool-heavy' },
  { value: 'probe', label: 'Behaviour probe' },
]

/** Probe cases come from GET /eval/probes; the rest are mined from history. */
export const PROBE = 'probe'

const LABEL = Object.fromEntries(CATEGORIES.map(c => [c.value, c.label]))

export function categoryLabel(slug) {
  return LABEL[slug] || slug
}

/**
 * The history kinds an agent cannot produce, each with the reason, from its
 * GET /agents/{name} detail. A missing field (an older server) or no detail at
 * all says nothing, so every kind stays applicable rather than hidden.
 */
export function inapplicableKinds(detail) {
  const out = {}
  if (!detail) return out
  if (Array.isArray(detail.command_skills) && detail.command_skills.length === 0) {
    out.skill_command = 'no command skills'
  }
  if (Array.isArray(detail.schedules) && detail.schedules.length === 0) {
    out.scheduled = 'no schedules'
  }
  if (detail.has_tools === false) out.tool_heavy = 'no tools'
  return out
}

/** Counts tasks per category slug, with every known category present. */
export function countByCategory(tasks) {
  const counts = Object.fromEntries(CATEGORIES.map(c => [c.value, 0]))
  for (const t of tasks || []) {
    counts[t.category] = (counts[t.category] || 0) + 1
  }
  return counts
}
