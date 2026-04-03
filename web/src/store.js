import { writable, derived } from 'svelte/store'

// The API key entered by the user, persisted to localStorage.
function createTokenStore() {
  const stored = localStorage.getItem('dk_token') || ''
  const { subscribe, set } = writable(stored)
  return {
    subscribe,
    set(value) {
      localStorage.setItem('dk_token', value)
      set(value)
    },
    clear() {
      localStorage.removeItem('dk_token')
      set('')
    },
  }
}

export const token = createTokenStore()

// Auth mode: 'token' (API key), 'session' (cookie-based), or null.
export const authMode = writable(null)

// true when the user has a token stored or is session-authenticated.
export const isAuthenticated = derived(
  [token, authMode],
  ([$t, $m]) => $t.length > 0 || $m === 'session'
)
