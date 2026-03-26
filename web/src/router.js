import { writable } from 'svelte/store'

function parseHash() {
  const h = window.location.hash
  // Strip leading '#/' or '#', default to 'overview'
  return h.replace(/^#\/?/, '') || 'overview'
}

export const currentRoute = writable(parseHash())

window.addEventListener('hashchange', () => {
  currentRoute.set(parseHash())
})

export function navigate(path) {
  window.location.hash = '/' + path
}
