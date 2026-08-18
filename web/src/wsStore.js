import { writable, derived, get } from 'svelte/store'
import { token, authMode } from './store.js'
import { DenkeeperWS } from './ws.js'

/**
 * The credential the WS URL is built from — 'session' for cookie auth, the API
 * key for token auth, '' when unauthenticated. Mirrors `_buildURL`, so a change
 * here is exactly a change in how the upgrade would authenticate.
 */
const credential = derived(
  [token, authMode],
  ([$t, $m]) => ($m === 'session' ? 'session' : $t)
)

/**
 * Connection status store.
 * Values: 'disconnected', 'connecting', 'connected', 'reconnecting', 'sse_fallback'
 */
export const wsStatus = writable('disconnected')

/**
 * Panic status store — updated when the server broadcasts a panic_status frame.
 */
export const panicStatus = writable({ active: false, message: '' })

/** Stores for per-session event routing. sessionID -> callback */
const sessionHandlers = new Map()

/** Subscribers for cross-adapter activity broadcasts. */
const activityCallbacks = new Set()

/** Register a handler for events on a specific session. */
export function onSessionEvent(sessionID, handler) {
  sessionHandlers.set(sessionID, handler)
}

/** Unregister a session handler. */
export function offSessionEvent(sessionID) {
  sessionHandlers.delete(sessionID)
}

/**
 * Notify all pending session handlers that the connection was lost.
 * This prevents sendViaWS promises from hanging forever.
 */
export function failAllSessionHandlers() {
  for (const [id, handler] of sessionHandlers) {
    handler({ type: 'error', session_id: id, message: 'WebSocket disconnected' })
  }
  sessionHandlers.clear()
}

/** Singleton WebSocket client instance. */
let wsClient = null

/** Get or create the WS client singleton. */
export function getWSClient() {
  if (wsClient) return wsClient

  wsClient = new DenkeeperWS({
    getToken: () => get(token),
    getAuthMode: () => get(authMode),
    onEvent: (frame) => {
      // Route to session-specific handler if one exists.
      if (frame.session_id && sessionHandlers.has(frame.session_id)) {
        sessionHandlers.get(frame.session_id)(frame)
        return
      }
      // Fallback: route to '__pending__' handler for new sessions where
      // the server assigned a session ID the client hasn't seen yet.
      if (frame.session_id && sessionHandlers.has('__pending__')) {
        sessionHandlers.get('__pending__')(frame)
        return
      }
      // Handle cross-adapter activity broadcasts.
      if (frame.type === 'activity') {
        activityCallbacks.forEach(cb => cb(frame))
        return
      }
      // Handle panic status broadcasts.
      if (frame.type === 'panic_status') {
        panicStatus.set({ active: frame.active, message: frame.message || '' })
        return
      }
      // Frames without a registered session are silently dropped.
    },
    onStatus: (status) => {
      wsStatus.set(status)
      // When the WS disconnects or falls back, reject any pending chat promises
      // so the UI doesn't get stuck waiting for a done frame that will never arrive.
      if (status === 'disconnected' || status === 'reconnecting' || status === 'sse_fallback') {
        failAllSessionHandlers()
      }
    },
  })

  return wsClient
}

/** Last credential the socket was connected with; '' means none. */
let lastCredential = ''

/** Unsubscribe for the credential watcher, null when not watching. */
let credentialUnsub = null

/**
 * Start the WS connection and keep it tracking the credential. Call once on app
 * startup.
 *
 * Connecting is gated on having a credential: an upgrade sent before login 401s,
 * and three of those exhaust the reconnect budget and strand the session on SSE
 * until a reload. The watcher also covers the reverse transition (logout) and a
 * key swap, resetting the budget so a stale failure count can't leak across
 * credentials.
 */
export function initWS() {
  const client = getWSClient()
  if (credentialUnsub) return client

  credentialUnsub = credential.subscribe((cred) => {
    if (cred === lastCredential) return
    lastCredential = cred
    if (!cred) {
      client.close()
      return
    }
    client.reset()
    client.connect()
  })

  return client
}

/** Subscribe to cross-adapter activity notifications. Returns an unsubscribe function. */
export function onActivity(cb) {
  activityCallbacks.add(cb)
  return () => activityCallbacks.delete(cb)
}

/** Tear down the WS connection and stop tracking the credential. */
export function destroyWS() {
  if (credentialUnsub) {
    credentialUnsub()
    credentialUnsub = null
  }
  lastCredential = ''
  if (wsClient) {
    wsClient.close()
  }
}
