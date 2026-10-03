import { describe, test, expect, beforeEach } from 'vitest'
import { get } from 'svelte/store'
import { http, HttpResponse } from 'msw'
import { server } from '../test/server.js'
import { token, authMode } from '../store.js'
import { setup, wizardOpen, showSetupReminder, refreshSetup, skipSetup, completeSetup, nextStep } from '../setupStore.js'

function wizard(overrides = {}) {
  return {
    wizard_completed: overrides.completed ?? true,
    wizard: {
      completed: true,
      skipped: false,
      agent: '',
      steps: [
        { id: 'provider', done: false },
        { id: 'agent', done: false },
        { id: 'persona', done: false },
        { id: 'chat_app', done: false, optional: true },
      ],
      done_count: 0,
      total: 4,
      restart_required: false,
      restart: { available: true, managed: false },
      ...overrides,
    },
  }
}

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  wizardOpen.set(false)
})

describe('setupStore', () => {
  test('refreshSetup maps the wizard block', async () => {
    server.use(http.get('/api/v1/onboarding', () => HttpResponse.json(wizard({ skipped: true, done_count: 1, restart_required: true }))))
    const state = await refreshSetup()
    expect(state).toMatchObject({ loaded: true, completed: true, skipped: true, doneCount: 1, total: 4, restartRequired: true })
    expect(get(setup).steps).toHaveLength(4)
  })

  test('refreshSetup resolves null when onboarding is not readable', async () => {
    server.use(http.get('/api/v1/onboarding', () => HttpResponse.json({ error: 'forbidden' }, { status: 403 })))
    expect(await refreshSetup()).toBeNull()
  })

  test('the reminder shows after a skip until the required steps are done', async () => {
    server.use(http.get('/api/v1/onboarding', () => HttpResponse.json(wizard({ skipped: true }))))
    await refreshSetup()
    expect(get(showSetupReminder)).toBe(true)

    const done = id => ({ id, done: true })
    server.use(http.get('/api/v1/onboarding', () => HttpResponse.json(wizard({
      skipped: true, steps: [done('provider'), done('agent'), done('persona'), { id: 'chat_app', done: false, optional: true }],
    }))))
    await refreshSetup()
    expect(get(showSetupReminder)).toBe(false)
  })

  test('no reminder when the wizard was finished, not skipped', async () => {
    server.use(http.get('/api/v1/onboarding', () => HttpResponse.json(wizard())))
    await refreshSetup()
    expect(get(showSetupReminder)).toBe(false)
  })

  test('skipSetup closes the wizard only when the server records it', async () => {
    wizardOpen.set(true)
    server.use(http.post('/api/v1/onboarding/wizard-skip', () => HttpResponse.json({ error: 'nope' }, { status: 500 })))
    await expect(skipSetup()).rejects.toThrow('nope')
    expect(get(wizardOpen)).toBe(true)

    server.use(http.post('/api/v1/onboarding/wizard-skip', () => new HttpResponse(null, { status: 204 })))
    await skipSetup()
    expect(get(wizardOpen)).toBe(false)
  })

  test('completeSetup closes the wizard', async () => {
    wizardOpen.set(true)
    await completeSetup()
    expect(get(wizardOpen)).toBe(false)
  })

  test('nextStep skips done and optional steps', () => {
    expect(nextStep([{ id: 'provider', done: true }, { id: 'agent', done: false }])).toBe('agent')
    expect(nextStep([{ id: 'provider', done: true }, { id: 'chat_app', done: false, optional: true }])).toBe('')
  })
})
