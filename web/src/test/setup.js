import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/svelte'
import { afterEach, beforeAll, afterAll } from 'vitest'
import { server } from './server.js'

// Always returns matches:false — tests run as "desktop". Mobile-conditional
// assertions need a per-test override (e.g. vi.fn returning matches:true).
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => {},
    addListener: () => {},
    removeListener: () => {},
  }),
})

// jsdom has no layout, so no ResizeObserver; Svelte's bind:clientWidth needs
// one. Elements report width 0, so components draw at their fallback size.
if (!('ResizeObserver' in window)) {
  window.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

// Node 22+ ships built-in localStorage without .clear().
// Provide a polyfill that removes all keys.
function clearStorage(storage) {
  if (typeof storage.clear === 'function') {
    storage.clear()
  } else {
    const keys = []
    for (let i = 0; i < storage.length; i++) keys.push(storage.key(i))
    keys.forEach(k => storage.removeItem(k))
  }
}

beforeAll(() => server.listen({ onUnhandledRequest: 'warn' }))

afterEach(() => {
  server.resetHandlers()
  cleanup()
  clearStorage(localStorage)
  clearStorage(sessionStorage)
  window.location.hash = ''
})

afterAll(() => server.close())
