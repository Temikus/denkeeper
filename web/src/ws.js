/**
 * WebSocket client for Denkeeper with auto-reconnect and SSE fallback.
 *
 * State machine: disconnected -> connecting -> connected -> reconnecting -> connected | sse_fallback
 */

const BACKOFF_INITIAL = 1000
const BACKOFF_MAX = 30000
const MAX_RECONNECT_ATTEMPTS = 3

export class DenkeeperWS {
  /**
   * @param {Object} options
   * @param {() => string} options.getToken - returns the current auth token
   * @param {() => string} options.getAuthMode - returns 'session' or 'token'
   * @param {(evt: Object) => void} options.onEvent - called for each server frame
   * @param {(status: string) => void} options.onStatus - called when connection status changes
   */
  constructor({ getToken, getAuthMode, onEvent, onStatus }) {
    this._getToken = getToken
    this._getAuthMode = getAuthMode
    this._onEvent = onEvent
    this._onStatus = onStatus
    this._ws = null
    this._status = 'disconnected'
    this._reconnectAttempts = 0
    this._reconnectTimer = null
    this._intentionalClose = false
  }

  get status() {
    return this._status
  }

  _setStatus(s) {
    this._status = s
    this._onStatus(s)
  }

  /** Build the WebSocket URL, converting http(s) to ws(s). */
  _buildURL() {
    const loc = window.location
    const proto = loc.protocol === 'https:' ? 'wss:' : 'ws:'
    let url = `${proto}//${loc.host}/api/v1/ws`

    // For token auth, pass token as query param (browsers can't set WS headers).
    if (this._getAuthMode() !== 'session') {
      const tok = this._getToken()
      if (tok) {
        url += `?token=${encodeURIComponent(tok)}`
      }
    }
    return url
  }

  /** Attempt to connect. */
  connect() {
    if (this._ws) return
    this._intentionalClose = false
    this._setStatus('connecting')

    try {
      this._ws = new WebSocket(this._buildURL())
    } catch (e) {
      this._handleFailure()
      return
    }

    this._ws.onopen = () => {
      this._reconnectAttempts = 0
      this._setStatus('connected')
    }

    this._ws.onmessage = (evt) => {
      try {
        const frame = JSON.parse(evt.data)
        this._onEvent(frame)
      } catch (_) {
        // Ignore malformed frames.
      }
    }

    this._ws.onclose = (evt) => {
      this._ws = null
      if (this._intentionalClose) {
        this._setStatus('disconnected')
        return
      }
      // Code 1008 = policy violation (auth revoked) — don't reconnect.
      if (evt.code === 1008) {
        this._setStatus('disconnected')
        return
      }
      this._handleFailure()
    }

    this._ws.onerror = () => {
      // onclose will fire after this — let it handle reconnection.
    }
  }

  _handleFailure() {
    this._reconnectAttempts++
    if (this._reconnectAttempts > MAX_RECONNECT_ATTEMPTS) {
      this._setStatus('sse_fallback')
      return
    }
    this._reconnect()
  }

  _reconnect() {
    this._setStatus('reconnecting')
    const delay = Math.min(
      BACKOFF_INITIAL * Math.pow(2, this._reconnectAttempts - 1),
      BACKOFF_MAX
    )
    this._reconnectTimer = setTimeout(() => {
      this._reconnectTimer = null
      this.connect()
    }, delay)
  }

  /** Send a JSON frame to the server. */
  send(frame) {
    if (!this._ws || this._ws.readyState !== WebSocket.OPEN) return false
    this._ws.send(JSON.stringify(frame))
    return true
  }

  /** Gracefully close the connection. */
  close() {
    this._intentionalClose = true
    if (this._reconnectTimer) {
      clearTimeout(this._reconnectTimer)
      this._reconnectTimer = null
    }
    if (this._ws) {
      this._ws.close(1000, 'client closing')
      this._ws = null
    }
    this._setStatus('disconnected')
  }

  /** Reset reconnect state and try WS again (e.g., after auth change). */
  reset() {
    this.close()
    this._reconnectAttempts = 0
    this._intentionalClose = false
  }
}
