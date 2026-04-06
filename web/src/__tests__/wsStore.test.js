import { describe, test, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

// Track DenkeeperWS constructor calls and instances
const mockConnect = vi.fn()
const mockClose = vi.fn()
const mockWSSend = vi.fn(() => true)
let capturedOptions = null

vi.mock('../ws.js', () => ({
  DenkeeperWS: vi.fn((opts) => {
    capturedOptions = opts
    return { connect: mockConnect, close: mockClose, send: mockWSSend }
  }),
}))

const { wsStatus, onSessionEvent, offSessionEvent, failAllSessionHandlers, getWSClient, initWS, destroyWS } = await import('../wsStore.js')

beforeEach(() => {
  mockConnect.mockReset()
  mockClose.mockReset()
  mockWSSend.mockReset().mockReturnValue(true)
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
  test('initWS calls connect on the client', () => {
    initWS()
    expect(mockConnect).toHaveBeenCalled()
  })

  test('destroyWS calls close on the client', () => {
    getWSClient() // ensure client exists
    destroyWS()
    expect(mockClose).toHaveBeenCalled()
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
