import { writable, get } from 'svelte/store'
import { token, authMode } from './store.js'
import { DenkeeperWS } from './ws.js'

/**
 * Connection status store.
 * Values: 'disconnected', 'connecting', 'connected', 'reconnecting', 'sse_fallback'
 */
export const wsStatus = writable('disconnected')

/** Stores for per-session event routing. sessionID -> callback */
const sessionHandlers = new Map()

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

/** Initialize the WS connection. Call once on app startup. */
export function initWS() {
  const client = getWSClient()
  client.connect()
  return client
}

/** Tear down the WS connection. */
export function destroyWS() {
  if (wsClient) {
    wsClient.close()
  }
}
