import { describe, test, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { token, authMode } from '../store.js'

// Track DenkeeperWS constructor calls and instances
const mockConnect = vi.fn()
const mockClose = vi.fn()
const mockWSReset = vi.fn()
const mockWSSend = vi.fn(() => true)
let capturedOptions = null

vi.mock('../ws.js', () => ({
  DenkeeperWS: vi.fn(function (opts) {
    capturedOptions = opts
    this.connect = mockConnect
    this.close = mockClose
    this.reset = mockWSReset
    this.send = mockWSSend
  }),
}))

const mockPanicStatus = vi.fn(async () => ({ panicked: false, panic_time: '0001-01-01T00:00:00Z' }))

vi.mock('../api.js', () => ({
  api: { panicStatus: (...args) => mockPanicStatus(...args) },
}))

const { wsStatus, panicStatus, evalProgress, refreshPanicStatus, onSessionEvent, offSessionEvent, failAllSessionHandlers, getWSClient, initWS, destroyWS } = await import('../wsStore.js')

/** Drop the credential watcher and clear auth, so each test starts logged out. */
function resetAuth() {
  destroyWS()
  token.clear()
  authMode.set(null)
  mockConnect.mockReset()
  mockClose.mockReset()
  mockWSReset.mockReset()
}

beforeEach(() => {
  mockConnect.mockReset()
  mockClose.mockReset()
  mockWSReset.mockReset()
  mockWSSend.mockReset().mockReturnValue(true)
  mockPanicStatus.mockReset().mockResolvedValue({ panicked: false, panic_time: '0001-01-01T00:00:00Z' })
  panicStatus.set({ active: false, message: '', since: '' })
  evalProgress.set(new Map())
})

describe('getWSClient', () => {
  test('returns a singleton — same instance on repeated calls', () => {
    const a = getWSClient()
    const b = getWSClient()
    expect(a).toBe(b)
  })

  test('creates DenkeeperWS with onEvent and onStatus callbacks', () => {
    getWSClient()
    expect(capturedOptions).toBeTruthy()
    expect(typeof capturedOptions.onEvent).toBe('function')
    expect(typeof capturedOptions.onStatus).toBe('function')
    expect(typeof capturedOptions.getToken).toBe('function')
    expect(typeof capturedOptions.getAuthMode).toBe('function')
  })
})

describe('initWS / destroyWS lifecycle', () => {
  beforeEach(resetAuth)

  test('initWS calls connect on the client when a token is already stored', () => {
    token.set('stored-key')
    mockConnect.mockReset()
    initWS()
    expect(mockConnect).toHaveBeenCalled()
    resetAuth()
  })

  test('destroyWS calls close on the client', () => {
    getWSClient() // ensure client exists
    destroyWS()
    expect(mockClose).toHaveBeenCalled()
  })
})

describe('credential-gated connect', () => {
  beforeEach(resetAuth)

  test('initWS does not dial while unauthenticated', () => {
    initWS()
    expect(mockConnect).not.toHaveBeenCalled()
    resetAuth()
  })

  test('logging in with an API key after initWS connects the socket', () => {
    initWS()
    expect(mockConnect).not.toHaveBeenCalled()

    token.set('login-key')

    expect(mockConnect).toHaveBeenCalledTimes(1)
    // reset() before connect clears the reconnect budget, so a pre-login
    // failure streak can't leave the session stranded on SSE.
    expect(mockWSReset).toHaveBeenCalledTimes(1)
    resetAuth()
  })

  test('session auth after initWS connects the socket', () => {
    initWS()
    authMode.set('session')
    expect(mockConnect).toHaveBeenCalledTimes(1)
    resetAuth()
  })

  test('swapping the API key redials with a fresh reconnect budget', () => {
    token.set('first-key')
    initWS()
    mockConnect.mockReset()
    mockWSReset.mockReset()

    token.set('second-key')

    expect(mockWSReset).toHaveBeenCalledTimes(1)
    expect(mockConnect).toHaveBeenCalledTimes(1)
    resetAuth()
  })

  test('re-setting the same credential does not redial', () => {
    token.set('same-key')
    initWS()
    mockConnect.mockReset()

    token.set('same-key')

    expect(mockConnect).not.toHaveBeenCalled()
    resetAuth()
  })

  test('logging out closes the socket without redialling', () => {
    token.set('bye-key')
    initWS()
    mockConnect.mockReset()

    token.clear()
    authMode.set(null)

    expect(mockClose).toHaveBeenCalled()
    expect(mockConnect).not.toHaveBeenCalled()
    resetAuth()
  })
})

describe('wsStatus store', () => {
  test('initial value is disconnected', () => {
    expect(get(wsStatus)).toBe('disconnected')
  })

  test('onStatus callback updates wsStatus store', () => {
    getWSClient()
    capturedOptions.onStatus('connected')
    expect(get(wsStatus)).toBe('connected')

    capturedOptions.onStatus('reconnecting')
    expect(get(wsStatus)).toBe('reconnecting')

    // Reset for other tests
    capturedOptions.onStatus('disconnected')
  })
})

describe('session event routing', () => {
  beforeEach(() => {
    // Ensure client is initialized so capturedOptions is available
    getWSClient()
  })

  test('frames with registered session_id are routed to handler', () => {
    const handler = vi.fn()
    onSessionEvent('sess-1', handler)

    const frame = { type: 'content', text: 'hello', session_id: 'sess-1' }
    capturedOptions.onEvent(frame)

    expect(handler).toHaveBeenCalledWith(frame)
    offSessionEvent('sess-1')
  })

  test('frames with unregistered session_id are silently dropped', () => {
    const handler = vi.fn()
    onSessionEvent('sess-1', handler)

    capturedOptions.onEvent({ type: 'content', text: 'hello', session_id: 'sess-unknown' })

    expect(handler).not.toHaveBeenCalled()
    offSessionEvent('sess-1')
  })

  test('frames without session_id are silently dropped', () => {
    const handler = vi.fn()
    onSessionEvent('sess-1', handler)

    capturedOptions.onEvent({ type: 'ping' })

    expect(handler).not.toHaveBeenCalled()
    offSessionEvent('sess-1')
  })

  test('offSessionEvent unregisters the handler', () => {
    const handler = vi.fn()
    onSessionEvent('sess-1', handler)
    offSessionEvent('sess-1')

    capturedOptions.onEvent({ type: 'content', text: 'hello', session_id: 'sess-1' })

    expect(handler).not.toHaveBeenCalled()
  })

  test('multiple sessions can be registered independently', () => {
    const handler1 = vi.fn()
    const handler2 = vi.fn()
    onSessionEvent('sess-a', handler1)
    onSessionEvent('sess-b', handler2)

    capturedOptions.onEvent({ type: 'content', text: 'a', session_id: 'sess-a' })
    capturedOptions.onEvent({ type: 'content', text: 'b', session_id: 'sess-b' })

    expect(handler1).toHaveBeenCalledTimes(1)
    expect(handler2).toHaveBeenCalledTimes(1)
    expect(handler1.mock.calls[0][0].text).toBe('a')
    expect(handler2.mock.calls[0][0].text).toBe('b')

    offSessionEvent('sess-a')
    offSessionEvent('sess-b')
  })

  test('registering a new handler for same session replaces previous', () => {
    const handler1 = vi.fn()
    const handler2 = vi.fn()
    onSessionEvent('sess-replace', handler1)
    onSessionEvent('sess-replace', handler2)

    capturedOptions.onEvent({ type: 'content', text: 'hello', session_id: 'sess-replace' })

    expect(handler1).not.toHaveBeenCalled()
    expect(handler2).toHaveBeenCalledTimes(1)

    offSessionEvent('sess-replace')
  })
})

describe('failAllSessionHandlers', () => {
  beforeEach(() => {
    getWSClient()
  })

  test('sends error event to all registered session handlers', () => {
    const handler1 = vi.fn()
    const handler2 = vi.fn()
    onSessionEvent('sess-a', handler1)
    onSessionEvent('sess-b', handler2)

    failAllSessionHandlers()

    expect(handler1).toHaveBeenCalledWith({
      type: 'error',
      session_id: 'sess-a',
      message: 'WebSocket disconnected',
    })
    expect(handler2).toHaveBeenCalledWith({
      type: 'error',
      session_id: 'sess-b',
      message: 'WebSocket disconnected',
    })
  })

  test('clears all handlers after failing them', () => {
    const handler = vi.fn()
    onSessionEvent('sess-1', handler)

    failAllSessionHandlers()
    handler.mockReset()

    // Event after clear should not be routed
    capturedOptions.onEvent({ type: 'content', text: 'late', session_id: 'sess-1' })
    expect(handler).not.toHaveBeenCalled()
  })

  test('no-ops when no handlers registered', () => {
    // Should not throw
    expect(() => failAllSessionHandlers()).not.toThrow()
  })
})

describe('onStatus triggers failAllSessionHandlers', () => {
  beforeEach(() => {
    getWSClient()
  })

  test('disconnected status fails all session handlers', () => {
    const handler = vi.fn()
    onSessionEvent('sess-dc', handler)

    capturedOptions.onStatus('disconnected')

    expect(handler).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'error', session_id: 'sess-dc' })
    )
  })

  test('reconnecting status fails all session handlers', () => {
    const handler = vi.fn()
    onSessionEvent('sess-rc', handler)

    capturedOptions.onStatus('reconnecting')

    expect(handler).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'error', session_id: 'sess-rc' })
    )
  })

  test('sse_fallback status fails all session handlers', () => {
    const handler = vi.fn()
    onSessionEvent('sess-fb', handler)

    capturedOptions.onStatus('sse_fallback')

    expect(handler).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'error', session_id: 'sess-fb' })
    )
  })

  test('connected status does not fail session handlers', () => {
    const handler = vi.fn()
    onSessionEvent('sess-ok', handler)

    capturedOptions.onStatus('connected')

    expect(handler).not.toHaveBeenCalled()
    offSessionEvent('sess-ok')
  })
})


describe('panic status hydration', () => {
  beforeEach(() => {
    getWSClient()
  })

  test('refreshPanicStatus reflects an active panic from the server', async () => {
    mockPanicStatus.mockResolvedValue({ panicked: true, panic_time: '2026-08-18T10:00:00Z' })

    await refreshPanicStatus()

    expect(get(panicStatus).active).toBe(true)
    expect(get(panicStatus).since).toBe('2026-08-18T10:00:00Z')
    expect(get(panicStatus).message).not.toBe('')
  })

  test('refreshPanicStatus drops the zero panic_time when not panicked', async () => {
    panicStatus.set({ active: true, message: 'stale', since: '2026-08-18T10:00:00Z' })
    mockPanicStatus.mockResolvedValue({ panicked: false, panic_time: '0001-01-01T00:00:00Z' })

    await refreshPanicStatus()

    expect(get(panicStatus)).toEqual({ active: false, message: '', since: '' })
  })

  test('a failed fetch leaves the store untouched', async () => {
    panicStatus.set({ active: true, message: 'paused', since: '2026-08-18T10:00:00Z' })
    mockPanicStatus.mockRejectedValue(new Error('Unauthorized'))

    await refreshPanicStatus()

    expect(get(panicStatus).active).toBe(true)
  })

  // Hydration hangs off the credential rather than app startup: a socket that
  // never establishes still has to render the right button, but a fetch fired
  // before login could only 401.
  test('a credential appearing hydrates panic state without waiting for a frame', () => {
    resetAuth()
    initWS()
    expect(mockPanicStatus).not.toHaveBeenCalled()

    token.set('login-key')
    expect(mockPanicStatus).toHaveBeenCalled()
  })

  test('connecting and reconnecting re-read panic state; a drop does not', () => {
    capturedOptions.onStatus('connected')
    expect(mockPanicStatus).toHaveBeenCalledTimes(1)

    capturedOptions.onStatus('reconnecting')
    expect(mockPanicStatus).toHaveBeenCalledTimes(1)

    capturedOptions.onStatus('connected')
    expect(mockPanicStatus).toHaveBeenCalledTimes(2)

    capturedOptions.onStatus('sse_fallback')
    expect(mockPanicStatus).toHaveBeenCalledTimes(3)

    capturedOptions.onStatus('disconnected')
    expect(mockPanicStatus).toHaveBeenCalledTimes(3)
  })

  test('a panic_status frame carries the panic time', () => {
    capturedOptions.onEvent({
      type: 'panic_status',
      active: true,
      message: 'Emergency stop triggered',
      since: '2026-08-18T10:00:00Z',
    })

    expect(get(panicStatus)).toEqual({
      active: true,
      message: 'Emergency stop triggered',
      since: '2026-08-18T10:00:00Z',
    })
  })

  test('a resume frame clears the panic time', () => {
    panicStatus.set({ active: true, message: 'paused', since: '2026-08-18T10:00:00Z' })

    capturedOptions.onEvent({ type: 'panic_status', active: false, message: 'Processing resumed' })

    expect(get(panicStatus)).toEqual({ active: false, message: 'Processing resumed', since: '' })
  })

  test('a frame landing mid-fetch wins over the older in-flight response', async () => {
    let release
    mockPanicStatus.mockReturnValue(new Promise((resolve) => {
      release = () => resolve({ panicked: false, panic_time: '0001-01-01T00:00:00Z' })
    }))

    const pending = refreshPanicStatus()

    // Panic is raised while the status request is still in flight.
    capturedOptions.onEvent({
      type: 'panic_status',
      active: true,
      message: 'Emergency stop triggered',
      since: '2026-08-18T10:00:00Z',
    })

    release()
    await pending

    expect(get(panicStatus).active).toBe(true)
    expect(get(panicStatus).since).toBe('2026-08-18T10:00:00Z')
  })
})

describe('eval progress frames', () => {
  beforeEach(() => {
    getWSClient()
  })

  test('an eval_progress frame lands in the store keyed by run_id', () => {
    capturedOptions.onEvent({
      type: 'eval_progress',
      run_id: 7,
      status: 'running',
      samples_done: 3,
      samples_total: 20,
      cost_spent: 0.12,
      cost_cap: 2,
      eta_seconds: 90,
    })

    const frame = get(evalProgress).get(7)
    expect(frame.status).toBe('running')
    expect(frame.samples_done).toBe(3)
    expect(frame.eta_seconds).toBe(90)
  })

  test('a later frame for the same run replaces the earlier one', () => {
    capturedOptions.onEvent({ type: 'eval_progress', run_id: 7, status: 'running', samples_done: 3, samples_total: 20 })
    capturedOptions.onEvent({ type: 'eval_progress', run_id: 7, status: 'done', samples_done: 20, samples_total: 20 })

    expect(get(evalProgress).size).toBe(1)
    expect(get(evalProgress).get(7).status).toBe('done')
    expect(get(evalProgress).get(7).samples_done).toBe(20)
  })

  test('runs are tracked independently', () => {
    capturedOptions.onEvent({ type: 'eval_progress', run_id: 7, status: 'running', samples_done: 3 })
    capturedOptions.onEvent({ type: 'eval_progress', run_id: 8, status: 'capped', samples_done: 11 })

    expect(get(evalProgress).get(7).samples_done).toBe(3)
    expect(get(evalProgress).get(8).status).toBe('capped')
  })

  // The store publishes a new Map rather than mutating in place, so Svelte
  // subscribers actually see the update.
  test('each frame publishes a new Map instance', () => {
    capturedOptions.onEvent({ type: 'eval_progress', run_id: 7, status: 'running' })
    const first = get(evalProgress)
    capturedOptions.onEvent({ type: 'eval_progress', run_id: 7, status: 'done' })

    expect(get(evalProgress)).not.toBe(first)
  })

  test('unknown session-less frames are still dropped', () => {
    capturedOptions.onEvent({ type: 'something_new', run_id: 7 })

    expect(get(evalProgress).size).toBe(0)
  })
})
