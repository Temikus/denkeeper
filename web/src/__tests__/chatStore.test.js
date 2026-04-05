import { describe, test, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

// Mock wsStore before importing chatStore
const mockWsStatus = await (async () => {
  const { writable } = await import('svelte/store')
  return writable('disconnected')
})()

const mockSend = vi.fn(() => true)
const mockOnSessionEvent = vi.fn()
const mockOffSessionEvent = vi.fn()

vi.mock('../wsStore.js', () => ({
  wsStatus: mockWsStatus,
  getWSClient: vi.fn(() => ({ send: mockSend })),
  onSessionEvent: mockOnSessionEvent,
  offSessionEvent: mockOffSessionEvent,
}))

// Mock api
const mockStreamChat = vi.fn()
const mockSessionMessages = vi.fn()
const mockApproveApproval = vi.fn()
const mockDenyApproval = vi.fn()

vi.mock('../api.js', () => ({
  api: {
    streamChat: (...args) => mockStreamChat(...args),
    sessionMessages: (...args) => mockSessionMessages(...args),
    approveApproval: (...args) => mockApproveApproval(...args),
    denyApproval: (...args) => mockDenyApproval(...args),
  },
}))

const { chatState, sendMessage, loadSession, newSession, setAgent } = await import('../chatStore.js')

beforeEach(() => {
  newSession()
  mockStreamChat.mockReset()
  mockSessionMessages.mockReset()
  mockApproveApproval.mockReset()
  mockDenyApproval.mockReset()
  mockSend.mockReset().mockReturnValue(true)
  mockOnSessionEvent.mockReset()
  mockOffSessionEvent.mockReset()
  mockWsStatus.set('disconnected')
})

describe('sendMessage', () => {
  test('sets sending=true and appends user + agent messages', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone) => {
      // Check intermediate state
      const state = get(chatState)
      expect(state.sending).toBe(true)
      expect(state.messages).toHaveLength(2)
      expect(state.messages[0].role).toBe('user')
      expect(state.messages[0].text).toBe('hello')
      expect(state.messages[1].role).toBe('agent')
      expect(state.messages[1].streaming).toBe(true)
      onDone('sess-1')
    })

    await sendMessage('hello')
    const state = get(chatState)
    expect(state.sending).toBe(false)
    expect(state.sessionId).toBe('sess-1')
  })

  test('SSE path: calls api.streamChat when wsStatus is disconnected', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone) => {
      onChunk('Hi ')
      onChunk('there')
      onDone('sess-1')
    })

    await sendMessage('hello')
    expect(mockStreamChat).toHaveBeenCalled()
    const state = get(chatState)
    expect(state.messages[1].text).toBe('Hi there')
  })

  test('WS path: calls sendViaWS when wsStatus is connected', async () => {
    mockWsStatus.set('connected')

    // Register handler and simulate server response
    mockOnSessionEvent.mockImplementation((id, handler) => {
      // Simulate server responding
      setTimeout(() => {
        handler({ type: 'content', text: 'WS response', session_id: 'sess-ws' })
        handler({ type: 'done', session_id: 'sess-ws' })
      }, 0)
    })

    await sendMessage('hello')
    expect(mockSend).toHaveBeenCalled()
    expect(mockStreamChat).not.toHaveBeenCalled()
  })

  test('no-op on empty text', async () => {
    await sendMessage('   ')
    expect(get(chatState).messages).toHaveLength(0)
  })

  test('no-op when already sending', async () => {
    let resolveStream
    mockStreamChat.mockImplementation(() => new Promise(r => { resolveStream = r }))

    const p1 = sendMessage('first')
    await sendMessage('second') // should be ignored

    const state = get(chatState)
    // Only first message pair should be present
    expect(state.messages.filter(m => m.role === 'user')).toHaveLength(1)
    expect(state.messages[0].text).toBe('first')

    // Clean up
    resolveStream()
    await p1
  })

  test('handles error: sets error on chatState', async () => {
    mockStreamChat.mockRejectedValue(new Error('network fail'))

    await sendMessage('hello')
    const state = get(chatState)
    expect(state.error).toBe('network fail')
    expect(state.sending).toBe(false)
  })
})

describe('handleToolEvent via SSE path', () => {
  test('tool_start adds to toolCalls array', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_start', tool: 'web_search', round: 1 })
      onDone('sess-1')
    })

    await sendMessage('search')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.toolCalls).toHaveLength(1)
    expect(agentMsg.toolCalls[0].name).toBe('web_search')
    expect(agentMsg.toolCalls[0].status).toBe('running')
  })

  test('tool_end marks tool done with duration', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_start', tool: 'web_search', round: 1 })
      onToolEvent({ type: 'tool_end', tool: 'web_search', round: 1, duration_ms: 500 })
      onDone('sess-1')
    })

    await sendMessage('search')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.toolCalls[0].status).toBe('done')
    expect(agentMsg.toolCalls[0].duration).toBe(500)
  })

  test('tool_end marks error when present', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_start', tool: 'web_search', round: 1 })
      onToolEvent({ type: 'tool_end', tool: 'web_search', round: 1, error: 'timeout' })
      onDone('sess-1')
    })

    await sendMessage('search')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.toolCalls[0].status).toBe('error')
    expect(agentMsg.toolCalls[0].error).toBe('timeout')
  })

  test('tool_approval pending adds to approvals and sets status', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_approval', approval_id: 'a1', tool: 'web_search', text: 'search', approval_status: 'pending' })
      // Check status before done
      const agentMsg = get(chatState).messages[1]
      expect(agentMsg.status).toBe('Waiting for approval: web_search')
      expect(agentMsg.approvals).toHaveLength(1)
      expect(agentMsg.approvals[0].status).toBe('pending')
      onDone('sess-1')
    })

    await sendMessage('search')
  })

  test('tool_approval auto_approved adds with correct status', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_approval', approval_id: 'a1', tool: 'web_search', text: 'search', approval_status: 'auto_approved' })
      onDone('sess-1')
    })

    await sendMessage('search')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.approvals[0].status).toBe('auto_approved')
  })

  test('thinking sets status text', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'thinking', text: 'Analyzing...' })
      const agentMsg = get(chatState).messages[1]
      expect(agentMsg.status).toBe('Analyzing...')
      onDone('sess-1')
    })

    await sendMessage('think')
  })

  test('usage sets tokens and costUSD', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'usage', tokens: 150, cost_usd: 0.0045 })
      onDone('sess-1')
    })

    await sendMessage('hello')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.tokens).toBe(150)
    expect(agentMsg.costUSD).toBe(0.0045)
  })
})

describe('loadSession', () => {
  test('fetches messages and maps roles', async () => {
    mockSessionMessages.mockResolvedValue([
      { role: 'user', content: 'Hello' },
      { role: 'assistant', content: 'Hi there' },
    ])

    await loadSession('sess-1', 'default')
    const state = get(chatState)
    expect(state.messages).toHaveLength(2)
    expect(state.messages[0].role).toBe('user')
    expect(state.messages[1].role).toBe('agent')
    expect(state.messages[1].text).toBe('Hi there')
    expect(state.sessionId).toBe('sess-1')
  })

  test('sets restoring during fetch', async () => {
    let resolveMessages
    mockSessionMessages.mockImplementation(() => new Promise(r => { resolveMessages = r }))

    const p = loadSession('sess-1', 'default')
    expect(get(chatState).restoring).toBe(true)

    resolveMessages([])
    await p
    expect(get(chatState).restoring).toBe(false)
  })

  test('sets error on API failure', async () => {
    mockSessionMessages.mockRejectedValue(new Error('fetch failed'))

    await loadSession('sess-1', 'default')
    const state = get(chatState)
    expect(state.error).toBe('Failed to load session')
    expect(state.restoring).toBe(false)
  })

  test('no-op when sessionId is empty', async () => {
    await loadSession('', 'default')
    expect(mockSessionMessages).not.toHaveBeenCalled()
  })
})

describe('newSession', () => {
  test('clears sessionId, messages, error', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone) => {
      onDone('sess-1')
    })
    await sendMessage('hello')

    newSession()
    const state = get(chatState)
    expect(state.sessionId).toBe('')
    expect(state.messages).toHaveLength(0)
    expect(state.error).toBe('')
  })

  test('removes sessionStorage entry', () => {
    sessionStorage.setItem('dk_chat_session', '{"sessionId":"old"}')
    newSession()
    expect(sessionStorage.getItem('dk_chat_session')).toBeNull()
  })
})

describe('setAgent', () => {
  test('updates agent in chatState', () => {
    setAgent('helper')
    expect(get(chatState).agent).toBe('helper')
  })
})
