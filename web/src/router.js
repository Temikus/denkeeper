import { writable } from 'svelte/store'

// Splits '#/agents/pamela?card=permission' into the path and its query.
function parseHash() {
  const h = window.location.hash.replace(/^#\/?/, '')
  const i = h.indexOf('?')
  const path = i < 0 ? h : h.slice(0, i)
  return { path: path || 'overview', query: new URLSearchParams(i < 0 ? '' : h.slice(i + 1)) }
}

const initial = parseHash()

// The path only, so routes keep matching when a deep link adds a query.
export const currentRoute = writable(initial.path)
export const currentQuery = writable(initial.query)

window.addEventListener('hashchange', () => {
  const { path, query } = parseHash()
  currentRoute.set(path)
  currentQuery.set(query)
})

export function navigate(path) {
  window.location.hash = '/' + path
}
