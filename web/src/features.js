import { writable } from 'svelte/store'

export const RETRY_MS = 15000

/**
 * Server features that are off unless the config turns them on. Read from
 * /health, which needs no credential, so the nav is right before login too.
 * Everything starts off: a page the server can't serve must not flash in.
 */
export const features = writable({ tasks: false })

let retry = null

/**
 * Reads /health. A failed read is not "off": the store keeps its last value
 * and the check runs again after RETRY_MS until one succeeds.
 */
export async function refreshFeatures() {
  clearTimeout(retry)
  retry = null
  try {
    const res = await fetch('/api/v1/health')
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const h = await res.json()
    features.set({ tasks: !!h?.tasks_enabled })
  } catch {
    retry = setTimeout(refreshFeatures, RETRY_MS)
  }
}
