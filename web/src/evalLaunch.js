// The agent last compared on, so a return visit starts there.
const AGENT_KEY = 'dk_eval_agent'

export function recallAgent() {
  try {
    return localStorage.getItem(AGENT_KEY) || ''
  } catch {
    return ''
  }
}

export function rememberAgent(name) {
  try {
    if (name) localStorage.setItem(AGENT_KEY, name)
  } catch { /* blocked storage: the default order below still applies */ }
}

/**
 * The agent the launcher compares on: the one a link named, then the last one
 * used, then the first that no other agent names as its supervisor, then the
 * first. A supervisor only reviews tool calls, so its model is rarely the
 * question being asked.
 */
export function pickBaseAgent(agents, linked, remembered) {
  const has = (n) => !!n && agents.some(a => a.name === n)
  if (has(linked)) return linked
  if (has(remembered)) return remembered
  const supervisors = new Set(agents.map(a => a.supervisor).filter(Boolean))
  return (agents.find(a => !supervisors.has(a.name)) || agents[0])?.name || ''
}

/**
 * The run's size spelled out, e.g. "10 cases × 1 run × 2 models = 20 turns".
 * Empty when the case count is unknown.
 */
export function turnsLine({ tasks, k, variants = 2, whole = false }) {
  if (!tasks) return ''
  const cases = `${tasks} case${tasks === 1 ? '' : 's'}`
  const runs = `${k} run${k === 1 ? '' : 's'}`
  const turns = tasks * k * variants
  return `${whole ? 'All ' : ''}${cases} × ${runs} × ${variants} models = ${turns} turns`
}
