// Node 25+ provides a built-in `localStorage` backed by a file (set via
// --localstorage-file). When multiple vitest workers share the same backing
// file, cross-file test contamination can occur. Always replace both storage
// globals with isolated in-memory implementations so each test file gets
// a clean, independent store.

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

globalThis.localStorage = createStorage()
globalThis.sessionStorage = createStorage()
