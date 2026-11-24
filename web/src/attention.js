import { writable } from 'svelte/store'
import { api } from './api.js'
import { panicStatus } from './wsStore.js'

const EMPTY = () => ({ pendingApprovals: 0, unhealthyTools: [], deciderReviews: {} })

/**
 * What needs the operator: pending approvals, broken tool servers and shadow
 * decider approvals the supervisor did not make. Feeds the sidebar and tab-bar
 * badges and the top-bar health line.
 *
 * Polled because no WS frame announces a new approval. Each part fails on its
 * own: a key without the approvals, tools or audit scope keeps that part at
 * its default rather than hiding the others.
 */
export const attention = writable(EMPTY())

// Sum of deciderReviews ({agent: n}). Tolerates a store set without the key.
export function deciderReviewTotal(a) {
  return Object.values(a?.deciderReviews || {}).reduce((n, v) => n + (Number(v) || 0), 0)
}

export const ATTENTION_POLL_MS = 15000

const UNHEALTHY = new Set(['error', 'config_error'])

let timer = null
let panicUnsub = null

// A background tab skips its polls and catches up the moment it is shown again.
function refreshIfVisible() {
  if (!document.hidden) refreshAttention()
}

// Bumped by each refresh and by stopAttention, so a response that lands after
// a newer refresh, or after logout, is dropped instead of overwriting the store.
let gen = 0

export async function refreshAttention() {
  const mine = ++gen
  const [approvals, tools, reviews] = await Promise.allSettled([
    api.approvals('pending'),
    api.listTools(),
    api.deciderReviewCounts(),
  ])
  if (mine !== gen) return
  attention.update((a) => ({
    pendingApprovals: approvals.status === 'fulfilled' && Array.isArray(approvals.value)
      ? approvals.value.length
      : a.pendingApprovals,
    unhealthyTools: tools.status === 'fulfilled' && Array.isArray(tools.value?.tools)
      ? tools.value.tools.filter(t => UNHEALTHY.has(t.status)).map(t => t.name)
      : a.unhealthyTools,
    deciderReviews: reviews.status === 'fulfilled' && reviews.value && typeof reviews.value === 'object'
      ? reviews.value
      : (a.deciderReviews || {}),
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
  gen++
  // The next credential may lack the scope to refill these, and a failed
  // poll keeps the old value, so never carry them across a logout.
  attention.set(EMPTY())
}
