import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { http, HttpResponse } from 'msw'
import { server } from '../test/server.js'
import { features, refreshFeatures, RETRY_MS } from '../features.js'

beforeEach(() => {
  features.set({ tasks: false })
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => vi.useRealTimers())

describe('features', () => {
  test('reads tasks_enabled from /health', async () => {
    server.use(http.get('/api/v1/health', () => HttpResponse.json({ status: 'ok', tasks_enabled: true })))
    await refreshFeatures()
    expect(get(features).tasks).toBe(true)
  })

  test('a failed read retries instead of settling on off', async () => {
    let calls = 0
    server.use(http.get('/api/v1/health', () => {
      calls++
      return calls === 1
        ? HttpResponse.json({ error: 'starting' }, { status: 503 })
        : HttpResponse.json({ status: 'ok', tasks_enabled: true })
    }))
    await refreshFeatures()
    expect(get(features).tasks).toBe(false)
    await vi.advanceTimersByTimeAsync(RETRY_MS + 10)
    await vi.waitFor(() => expect(get(features).tasks).toBe(true))
    expect(calls).toBe(2)
  })
})
