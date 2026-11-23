import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { http, HttpResponse } from 'msw'
import { waitFor } from '@testing-library/svelte'
import { server } from '../test/server.js'
import { attention, refreshAttention, startAttention, stopAttention, deciderReviewTotal, ATTENTION_POLL_MS } from '../attention.js'
import { panicStatus } from '../wsStore.js'

const pending = (n) => Array.from({ length: n }, (_, i) => ({ id: `a${i}`, status: 'pending' }))

beforeEach(() => {
  attention.set({ pendingApprovals: 0, unhealthyTools: [] })
})

afterEach(() => {
  stopAttention()
  panicStatus.set({ active: false, message: '', since: '' })
})

describe('attention', () => {
  test('counts pending approvals and names broken tool servers', async () => {
    server.use(
      http.get('/api/v1/approvals', () => HttpResponse.json(pending(2))),
      // Same shape as handleListTools: {"tools": [...]}.
      http.get('/api/v1/tools', () => HttpResponse.json({ tools: [
        { name: 'web', status: 'connected' },
        { name: 'github', status: 'error' },
        { name: 'jira', status: 'config_error' },
        { name: 'kv', status: 'disabled' },
      ] })),
    )
    await refreshAttention()
    expect(get(attention)).toEqual({ pendingApprovals: 2, unhealthyTools: ['github', 'jira'], deciderReviews: {} })
  })

  test('the decider review counts land in the store', async () => {
    server.use(http.get('/api/v1/decider-reviews/unreviewed', () => HttpResponse.json({ default: 2, helper: 0 })))
    await refreshAttention()
    expect(get(attention).deciderReviews).toEqual({ default: 2, helper: 0 })
  })

  test('a forbidden review count keeps its last map and the other parts still update', async () => {
    attention.set({ pendingApprovals: 0, unhealthyTools: [], deciderReviews: { default: 1 } })
    server.use(
      http.get('/api/v1/decider-reviews/unreviewed', () => HttpResponse.json({ error: 'forbidden' }, { status: 403 })),
      http.get('/api/v1/approvals', () => HttpResponse.json(pending(1))),
      http.get('/api/v1/tools', () => HttpResponse.json({ tools: [{ name: 'github', status: 'error' }] })),
    )
    await refreshAttention()
    expect(get(attention)).toEqual({ pendingApprovals: 1, unhealthyTools: ['github'], deciderReviews: { default: 1 } })
  })

  test('deciderReviewTotal sums the map and treats a missing key as 0', () => {
    expect(deciderReviewTotal({ deciderReviews: { a: 2, b: 3 } })).toBe(5)
    expect(deciderReviewTotal({ pendingApprovals: 1, unhealthyTools: [] })).toBe(0)
  })

  test('asks only for pending approvals', async () => {
    let status
    server.use(http.get('/api/v1/approvals', ({ request }) => {
      status = new URL(request.url).searchParams.get('status')
      return HttpResponse.json([])
    }))
    await refreshAttention()
    expect(status).toBe('pending')
  })

  test('a forbidden half keeps its last value and the other half still updates', async () => {
    attention.set({ pendingApprovals: 3, unhealthyTools: [] })
    server.use(
      http.get('/api/v1/approvals', () => HttpResponse.json({ error: 'forbidden' }, { status: 403 })),
      http.get('/api/v1/tools', () => HttpResponse.json({ tools: [{ name: 'github', status: 'error' }] })),
    )
    await refreshAttention()
    expect(get(attention)).toEqual({ pendingApprovals: 3, unhealthyTools: ['github'], deciderReviews: {} })
  })

  test('re-reads when panic flips, since a stop aborts pending approvals', async () => {
    let calls = 0
    server.use(http.get('/api/v1/approvals', () => {
      calls++
      return HttpResponse.json(calls === 1 ? pending(2) : [])
    }))
    startAttention()
    await waitFor(() => expect(get(attention).pendingApprovals).toBe(2))

    panicStatus.set({ active: true, message: 'paused', since: '' })
    await waitFor(() => expect(get(attention).pendingApprovals).toBe(0))
  })

  // Every (re)connect re-sets panicStatus with an unchanged value.
  test('a reconnect that re-sets the same panic state does not refetch', async () => {
    let calls = 0
    server.use(http.get('/api/v1/approvals', () => { calls++; return HttpResponse.json([]) }))
    startAttention()
    await waitFor(() => expect(calls).toBe(1))

    panicStatus.set({ active: false, message: '', since: '' })
    panicStatus.set({ active: false, message: '', since: '' })
    await new Promise(r => setTimeout(r, 50))

    expect(calls).toBe(1)
  })

  test('a hidden tab skips polls and catches up when shown', async () => {
    let calls = 0
    server.use(http.get('/api/v1/approvals', () => { calls++; return HttpResponse.json([]) }))
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true)
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    try {
      startAttention()
      await vi.waitFor(() => expect(calls).toBe(1))

      vi.advanceTimersByTime(ATTENTION_POLL_MS * 3)
      await new Promise(r => setTimeout(r, 20))
      expect(calls).toBe(1)

      hidden.mockReturnValue(false)
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.waitFor(() => expect(calls).toBe(2))
    } finally {
      vi.useRealTimers()
    }
  })

  test('an older response that lands after a newer one is dropped', async () => {
    let release
    let calls = 0
    server.use(http.get('/api/v1/approvals', async () => {
      calls++
      if (calls === 1) await new Promise(r => { release = r })
      return HttpResponse.json(calls === 1 ? pending(5) : [])
    }))

    const slow = refreshAttention()
    await vi.waitFor(() => expect(release).toBeTypeOf('function'))
    await refreshAttention()
    expect(get(attention).pendingApprovals).toBe(0)

    release()
    await slow
    expect(get(attention).pendingApprovals).toBe(0)
  })

  test('stopping clears the counts and drops a refresh still in flight', async () => {
    let release
    server.use(http.get('/api/v1/approvals', async () => {
      await new Promise(r => { release = r })
      return HttpResponse.json(pending(4))
    }))
    attention.set({ pendingApprovals: 2, unhealthyTools: ['github'], deciderReviews: { default: 3 } })

    const inFlight = refreshAttention()
    await vi.waitFor(() => expect(release).toBeTypeOf('function'))
    stopAttention()
    const empty = { pendingApprovals: 0, unhealthyTools: [], deciderReviews: {} }
    expect(get(attention)).toEqual(empty)

    release()
    await inFlight
    expect(get(attention)).toEqual(empty)
  })
})
