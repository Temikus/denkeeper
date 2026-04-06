import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { DenkeeperWS } from '../ws.js'

// Mock WebSocket
class MockWebSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 3

  constructor(url) {
    this.url = url
    this.readyState = MockWebSocket.OPEN
    this.sent = []
    MockWebSocket.instances.push(this)
  }

  send(data) {
    this.sent.push(data)
  }

  close() {
    this.readyState = MockWebSocket.CLOSED
    // Simulate onclose firing after close() is called
    if (this.onclose) {
      setTimeout(() => this.onclose({ code: 1000, reason: '', wasClean: true }), 0)
    }
  }
}
MockWebSocket.instances = []

beforeEach(() => {
  MockWebSocket.instances = []
  vi.stubGlobal('WebSocket', MockWebSocket)
  vi.useFakeTimers()
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
})

function createWS(overrides = {}) {
  const onEvent = vi.fn()
  const onStatus = vi.fn()
  const ws = new DenkeeperWS({
    getToken: () => 'test-token',
    getAuthMode: () => 'token',
    onEvent,
    onStatus,
    ...overrides,
  })
  return { ws, onEvent, onStatus }
}

describe('connect', () => {
  test('transitions disconnected -> connecting -> connected on onopen', () => {
    const { ws, onStatus } = createWS()
    ws.connect()

    expect(onStatus).toHaveBeenCalledWith('connecting')

    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    expect(onStatus).toHaveBeenCalledWith('connected')
    expect(ws.status).toBe('connected')
  })

  test('includes token query param for token auth', () => {
    const { ws } = createWS()
    ws.connect()

    const url = MockWebSocket.instances[0].url
    expect(url).toContain('token=test-token')
  })

  test('omits token for session auth mode', () => {
    const { ws } = createWS({ getAuthMode: () => 'session' })
    ws.connect()

    const url = MockWebSocket.instances[0].url
    expect(url).not.toContain('token=')
  })

  test('no-ops if already connected', () => {
    const { ws } = createWS()
    ws.connect()
    ws.connect()
    expect(MockWebSocket.instances).toHaveLength(1)
  })
})

describe('connect timeout', () => {
  test('forces close if connection stays in CONNECTING state past timeout', () => {
    const { ws, onStatus } = createWS()
    ws.connect()

    const mockWs = MockWebSocket.instances[0]
    // Simulate staying in CONNECTING state (never fires onopen)
    mockWs.readyState = MockWebSocket.CONNECTING

    // Advance past the 5s connect timeout
    vi.advanceTimersByTime(5000)

    // Should have called close() on the stuck socket
    expect(mockWs.readyState).toBe(MockWebSocket.CLOSED)
  })

  test('does not force close if connection opens before timeout', () => {
    const { ws } = createWS()
    ws.connect()

    const mockWs = MockWebSocket.instances[0]
    // Connection opens immediately
    mockWs.onopen()

    // Advance past timeout — should be a no-op
    vi.advanceTimersByTime(5000)
    // Socket was OPEN after onopen, timeout should not have closed it
    expect(ws.status).toBe('connected')
  })

  test('close() clears connect timer', () => {
    const { ws } = createWS()
    ws.connect()

    const mockWs = MockWebSocket.instances[0]
    mockWs.readyState = MockWebSocket.CONNECTING

    // Close before timeout fires
    ws.close()

    // Advance past timeout — should not throw or change state
    vi.advanceTimersByTime(5000)
    expect(ws.status).toBe('disconnected')
  })
})

describe('message handling', () => {
  test('parses JSON and calls onEvent', () => {
    const { ws, onEvent } = createWS()
    ws.connect()
    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    mockWs.onmessage({ data: '{"type":"content","text":"hello"}' })
    expect(onEvent).toHaveBeenCalledWith({ type: 'content', text: 'hello' })
  })

  test('ignores malformed JSON', () => {
    const { ws, onEvent } = createWS()
    ws.connect()
    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    mockWs.onmessage({ data: 'not json' })
    expect(onEvent).not.toHaveBeenCalled()
  })
})

describe('reconnection', () => {
  test('reconnects on unexpected close with exponential backoff', () => {
    const { ws, onStatus } = createWS()
    ws.connect()
    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    // Simulate unexpected close
    mockWs.onclose({ code: 1006 })
    expect(onStatus).toHaveBeenCalledWith('reconnecting')

    // Advance by 1s (first backoff)
    vi.advanceTimersByTime(1000)
    expect(MockWebSocket.instances).toHaveLength(2) // reconnected
  })

  test('falls back to sse_fallback after max reconnect attempts', () => {
    const { ws, onStatus } = createWS()
    ws.connect()

    // Exhaust reconnect attempts (3 max)
    for (let i = 0; i < 3; i++) {
      const mockWs = MockWebSocket.instances[MockWebSocket.instances.length - 1]
      mockWs.onclose({ code: 1006 })
      if (i < 2) {
        vi.advanceTimersByTime(30000) // advance past any backoff
      }
    }

    // 4th failure should trigger sse_fallback
    const lastMock = MockWebSocket.instances[MockWebSocket.instances.length - 1]
    lastMock.onclose({ code: 1006 })

    expect(onStatus).toHaveBeenCalledWith('sse_fallback')
  })

  test('does not reconnect on code 1008 (auth revoked)', () => {
    const { ws, onStatus } = createWS()
    ws.connect()
    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    mockWs.onclose({ code: 1008 })
    expect(onStatus).toHaveBeenCalledWith('disconnected')
    expect(MockWebSocket.instances).toHaveLength(1)
  })

  test('does not reconnect on intentional close', () => {
    const { ws, onStatus } = createWS()
    ws.connect()
    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    ws.close()
    expect(onStatus).toHaveBeenCalledWith('disconnected')

    vi.advanceTimersByTime(30000)
    expect(MockWebSocket.instances).toHaveLength(1)
  })
})

describe('send', () => {
  test('returns true and sends JSON when connected', () => {
    const { ws } = createWS()
    ws.connect()
    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    const result = ws.send({ type: 'chat_request', message: 'hello' })
    expect(result).toBe(true)
    expect(mockWs.sent[0]).toBe(JSON.stringify({ type: 'chat_request', message: 'hello' }))
  })

  test('returns false when not connected', () => {
    const { ws } = createWS()
    const result = ws.send({ type: 'chat_request' })
    expect(result).toBe(false)
  })
})

describe('close', () => {
  test('transitions to disconnected and clears timers', () => {
    const { ws, onStatus } = createWS()
    ws.connect()
    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    ws.close()
    expect(ws.status).toBe('disconnected')
    expect(onStatus).toHaveBeenCalledWith('disconnected')
  })
})

describe('reset', () => {
  test('closes connection and clears reconnect state', () => {
    const { ws } = createWS()
    ws.connect()
    const mockWs = MockWebSocket.instances[0]
    mockWs.onopen()

    ws.reset()
    expect(ws.status).toBe('disconnected')

    // Should be able to connect again with fresh attempts
    ws.connect()
    expect(MockWebSocket.instances).toHaveLength(2)
  })
})
