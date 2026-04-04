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

// Theme: 'light' (default) or 'dark'. Persisted to localStorage, synced to <html> class.
function createThemeStore() {
  const stored = localStorage.getItem('dk_theme') || 'light'
  const { subscribe, set } = writable(stored)
  return {
    subscribe,
    toggle() {
      let next
      const unsub = subscribe(v => { next = v === 'dark' ? 'light' : 'dark' })
      unsub()
      localStorage.setItem('dk_theme', next)
      document.documentElement.classList.toggle('dark', next === 'dark')
      set(next)
    },
  }
}

export const theme = createThemeStore()

// Auth mode: 'token' (API key), 'session' (cookie-based), or null.
export const authMode = writable(null)

// true when the user has a token stored or is session-authenticated.
export const isAuthenticated = derived(
  [token, authMode],
  ([$t, $m]) => $t.length > 0 || $m === 'session'
)
