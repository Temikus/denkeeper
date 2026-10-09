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

/** Counts tasks per category slug, with every known category present. */
export function countByCategory(tasks) {
  const counts = Object.fromEntries(CATEGORIES.map(c => [c.value, 0]))
  for (const t of tasks || []) {
    counts[t.category] = (counts[t.category] || 0) + 1
  }
  return counts
}
