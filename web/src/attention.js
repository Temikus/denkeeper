import { writable } from 'svelte/store'
import { api } from './api.js'
import { panicStatus } from './wsStore.js'

/**
 * What needs the operator: pending approvals and broken tool servers. Feeds the
 * sidebar and tab-bar badges and the top-bar health line.
 *
 * Polled because no WS frame announces a new approval. Each half fails on its
 * own: a key without the approvals or tools scope keeps that half at its
 * default rather than hiding the other.
 */
export const attention = writable({ pendingApprovals: 0, unhealthyTools: [] })

export const ATTENTION_POLL_MS = 15000

const UNHEALTHY = new Set(['error', 'config_error'])

let timer = null
let panicUnsub = null

// A background tab skips its polls and catches up the moment it is shown again.
function refreshIfVisible() {
  if (!document.hidden) refreshAttention()
}

export async function refreshAttention() {
  const [approvals, tools] = await Promise.allSettled([
    api.approvals('pending'),
    api.listTools(),
  ])
  attention.update((a) => ({
    pendingApprovals: approvals.status === 'fulfilled' && Array.isArray(approvals.value)
      ? approvals.value.length
      : a.pendingApprovals,
    unhealthyTools: tools.status === 'fulfilled' && Array.isArray(tools.value)
      ? tools.value.filter(t => UNHEALTHY.has(t.status)).map(t => t.name)
      : a.unhealthyTools,
  }))
}

export function startAttention() {
  if (timer) return
  refreshAttention()
  timer = setInterval(refreshIfVisible, ATTENTION_POLL_MS)
  document.addEventListener('visibilitychange', refreshIfVisible)
  // A stop aborts every pending approval, so the count is stale the moment
  // panic flips either way. The store is re-set on every (re)connect with the
  // same value, so compare rather than react to each set.
  let wasActive = null
  panicUnsub = panicStatus.subscribe(({ active }) => {
    if (wasActive !== null && active !== wasActive) refreshAttention()
    wasActive = active
  })
}

export function stopAttention() {
  clearInterval(timer)
  timer = null
  document.removeEventListener('visibilitychange', refreshIfVisible)
  if (panicUnsub) panicUnsub()
  panicUnsub = null
}
