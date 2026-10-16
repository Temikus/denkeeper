import { writable } from 'svelte/store'
import { api } from './api.js'

/**
 * Server features that are off unless the config turns them on. Read from
 * /health, which needs no credential, so the nav is right before login too.
 * Everything starts off: a page the server can't serve must not flash in.
 */
export const features = writable({ tasks: false })

export async function refreshFeatures() {
  try {
    const h = await api.health()
    features.set({ tasks: !!h?.tasks_enabled })
  } catch {
    // Server unreachable: keep what we last knew.
  }
}
