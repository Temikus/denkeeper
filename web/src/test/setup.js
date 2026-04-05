import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/svelte'
import { afterEach, beforeAll, afterAll } from 'vitest'
import { server } from './server.js'

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
