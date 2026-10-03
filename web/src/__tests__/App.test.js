import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, waitFor } from '@testing-library/svelte'
import { get } from 'svelte/store'
import { http, HttpResponse, delay } from 'msw'
import { server } from '../test/server.js'
import { token, authMode } from '../store.js'
import { wizardOpen } from '../setupStore.js'

vi.mock('../wsStore.js', async () => {
  const { writable } = await import('svelte/store')
  return {
    wsStatus: writable('disconnected'),
    panicStatus: writable({ active: false, message: '', since: '' }),
    initWS: vi.fn(),
    destroyWS: vi.fn(),
    refreshPanicStatus: vi.fn(),
    getWSClient: vi.fn(() => ({ send: vi.fn(() => true) })),
    onSessionEvent: vi.fn(),
    offSessionEvent: vi.fn(),
    onActivity: vi.fn(() => vi.fn()),
  }
})

const App = (await import('../App.svelte')).default

const unfinished = {
  show_onboarding: true, steps: [], dismissed: false, wizard_completed: false,
  wizard: { completed: false, skipped: false, agent: '', steps: [], done_count: 0, total: 4, restart_required: false, restart: { available: false, managed: false } },
}

function logout() {
  token.clear()
  authMode.set(null)
}

beforeEach(() => {
  wizardOpen.set(false)
  token.set('test-key')
  authMode.set('token')
  server.use(http.get('/auth/session', () => HttpResponse.json({ authenticated: false })))
})

describe('App setup gating', () => {
  test('a setup check that returns after logout does not open the wizard', async () => {
    let answered = false
    server.use(http.get('/api/v1/onboarding', async () => {
      await delay(50)
      answered = true
      return HttpResponse.json(unfinished)
    }))
    render(App)
    logout()

    await waitFor(() => expect(answered).toBe(true))
    await new Promise(r => setTimeout(r, 20))
    expect(get(wizardOpen)).toBe(false)
  })

  test('logging out closes an open wizard', async () => {
    server.use(http.get('/api/v1/onboarding', () => HttpResponse.json(unfinished)))
    render(App)
    await waitFor(() => expect(get(wizardOpen)).toBe(true))

    logout()
    await waitFor(() => expect(get(wizardOpen)).toBe(false))
  })
})
