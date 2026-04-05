import { describe, test, expect, beforeEach } from 'vitest'
import { get } from 'svelte/store'
import { token, theme, authMode, isAuthenticated } from '../store.js'

beforeEach(() => {
  token.clear()
  authMode.set(null)
  // Reset theme to light (toggle from whatever state)
  if (get(theme) === 'dark') theme.toggle()
})

describe('token store', () => {
  test('set() persists to localStorage', () => {
    token.set('my-key')
    expect(get(token)).toBe('my-key')
    expect(localStorage.getItem('dk_token')).toBe('my-key')
  })

  test('clear() removes from localStorage and sets empty string', () => {
    token.set('abc')
    token.clear()
    expect(get(token)).toBe('')
    expect(localStorage.getItem('dk_token')).toBeNull()
  })
})

describe('theme store', () => {
  test('toggle switches light to dark', () => {
    expect(get(theme)).toBe('light')
    theme.toggle()
    expect(get(theme)).toBe('dark')
  })

  test('toggle switches dark back to light', () => {
    theme.toggle() // light -> dark
    theme.toggle() // dark -> light
    expect(get(theme)).toBe('light')
  })

  test('toggle persists to localStorage', () => {
    theme.toggle()
    expect(localStorage.getItem('dk_theme')).toBe('dark')
  })

  test('toggle updates document.documentElement classList', () => {
    theme.toggle()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    theme.toggle()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})

describe('isAuthenticated', () => {
  test('true when token is non-empty', () => {
    token.set('some-token')
    expect(get(isAuthenticated)).toBe(true)
  })

  test('true when authMode is session even without token', () => {
    authMode.set('session')
    expect(get(isAuthenticated)).toBe(true)
  })

  test('false when no token and authMode is not session', () => {
    expect(get(isAuthenticated)).toBe(false)
  })

  test('false when authMode is token but token is empty', () => {
    authMode.set('token')
    expect(get(isAuthenticated)).toBe(false)
  })
})
