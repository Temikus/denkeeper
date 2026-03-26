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

// true when the user has a token stored (does not validate the key server-side).
export const isAuthenticated = derived(token, $t => $t.length > 0)
