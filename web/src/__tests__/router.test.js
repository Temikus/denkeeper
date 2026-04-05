import { describe, test, expect, beforeEach } from 'vitest'
import { get } from 'svelte/store'
import { currentRoute, navigate } from '../router.js'

beforeEach(() => {
  window.location.hash = ''
  // Dispatch hashchange so the store picks up the reset
  window.dispatchEvent(new HashChangeEvent('hashchange'))
})

describe('navigate', () => {
  test('sets window.location.hash', () => {
    navigate('chat')
    expect(window.location.hash).toBe('#/chat')
  })
})

describe('currentRoute store', () => {
  test('defaults to overview when hash is empty', () => {
    expect(get(currentRoute)).toBe('overview')
  })

  test('updates on hashchange event', () => {
    window.location.hash = '#/approvals'
    window.dispatchEvent(new HashChangeEvent('hashchange'))
    expect(get(currentRoute)).toBe('approvals')
  })

  test('strips leading #/ from hash', () => {
    window.location.hash = '#/sessions'
    window.dispatchEvent(new HashChangeEvent('hashchange'))
    expect(get(currentRoute)).toBe('sessions')
  })

  test('handles nested routes', () => {
    window.location.hash = '#/agents/detail'
    window.dispatchEvent(new HashChangeEvent('hashchange'))
    expect(get(currentRoute)).toBe('agents/detail')
  })
})
