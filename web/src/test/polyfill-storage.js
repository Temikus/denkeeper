// Node 25+ provides a bare `localStorage` global that lacks the Web Storage
// API (getItem, setItem, removeItem, clear, key, length). When vitest's jsdom
// environment sets up window.localStorage, it doesn't always override the
// global `localStorage`. This polyfill bridges the gap.

function createStorage() {
  const store = new Map()
  return {
    getItem(key) { return store.has(key) ? store.get(key) : null },
    setItem(key, value) { store.set(key, String(value)) },
    removeItem(key) { store.delete(key) },
    clear() { store.clear() },
    key(index) { return [...store.keys()][index] ?? null },
    get length() { return store.size },
  }
}

// Only polyfill if the native localStorage lacks getItem (Node 25+).
if (typeof globalThis.localStorage === 'undefined' || typeof globalThis.localStorage.getItem !== 'function') {
  globalThis.localStorage = createStorage()
}
if (typeof globalThis.sessionStorage === 'undefined' || typeof globalThis.sessionStorage.getItem !== 'function') {
  globalThis.sessionStorage = createStorage()
}
