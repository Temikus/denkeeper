import { get } from 'svelte/store'
import { taskUpdates, wsStatus } from './wsStore.js'

export const POLL_MS = 15000
const DEBOUNCE_MS = 250

/**
 * Calls reload after task_updated frames (a burst becomes one call), when the
 * socket reconnects, and every POLL_MS while it is down, since frames sent
 * while away are never replayed. Returns a stop function.
 */
export function watchTasks(reload) {
  let timer = null
  const kick = () => {
    clearTimeout(timer)
    timer = setTimeout(reload, DEBOUNCE_MS)
  }

  let primed = false
  const stopTasks = taskUpdates.subscribe(() => {
    if (primed) kick()
    primed = true
  })

  let last = get(wsStatus)
  const stopStatus = wsStatus.subscribe(s => {
    if (s === 'connected' && last !== 'connected') kick()
    last = s
  })

  const poll = setInterval(() => {
    if (get(wsStatus) !== 'connected') reload()
  }, POLL_MS)

  return () => {
    clearTimeout(timer)
    clearInterval(poll)
    stopTasks()
    stopStatus()
  }
}
