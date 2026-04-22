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

const { chatState, sendMessage, loadSession, newSession, setAgent, initChat, resolveApprovalAction, pendingSkillTest } = await import('../chatStore.js')

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

  test('clears streaming flag on agent message after error', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onChunk('partial ')
      throw new Error('mid-stream crash')
    })

    await sendMessage('hello')
    const state = get(chatState)
    const agentMsg = state.messages[1]
    // streaming should be cleared by the finally block
    expect(agentMsg.streaming).toBe(false)
    expect(agentMsg.status).toBe('')
    expect(state.sending).toBe(false)
  })

  test('clears streaming flag even when stream ends without done event', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone) => {
      onChunk('response')
      // Stream ends without calling onDone — the finally block should clean up
    })

    await sendMessage('hello')
    const state = get(chatState)
    const agentMsg = state.messages[1]
    expect(agentMsg.streaming).toBe(false)
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

describe('initChat', () => {
  test('sets agent from first agent and marks initialized', async () => {
    // Reset initialized flag by forcing state
    chatState.update(s => ({ ...s, initialized: false }))
    mockSessionMessages.mockResolvedValue([])

    await initChat([{ name: 'myagent' }, { name: 'other' }])
    const state = get(chatState)
    expect(state.agent).toBe('myagent')
    expect(state.initialized).toBe(true)
  })

  test('no-op when already initialized', async () => {
    chatState.update(s => ({ ...s, initialized: false }))
    mockSessionMessages.mockResolvedValue([])
    await initChat([{ name: 'first' }])

    // Second call should be a no-op
    await initChat([{ name: 'second' }])
    expect(get(chatState).agent).toBe('first')
  })

  test('does not override agent when sessionId already set', async () => {
    chatState.update(s => ({ ...s, initialized: false, sessionId: 'existing', agent: 'preset' }))
    mockSessionMessages.mockResolvedValue([])

    await initChat([{ name: 'other' }])
    // Agent should remain 'preset' because sessionId was already set
    expect(get(chatState).agent).toBe('preset')
  })

  test('restores session from sessionStorage', async () => {
    chatState.update(s => ({ ...s, initialized: false }))
    sessionStorage.setItem('dk_chat_session', JSON.stringify({ sessionId: 'sess-saved', agent: 'saved-agent' }))
    mockSessionMessages.mockResolvedValue([
      { role: 'user', content: 'saved msg' },
      { role: 'assistant', content: 'saved reply' },
    ])

    await initChat([{ name: 'default' }])
    const state = get(chatState)
    expect(state.sessionId).toBe('sess-saved')
    expect(state.agent).toBe('saved-agent')
    expect(state.messages).toHaveLength(2)
  })

  test('handles malformed sessionStorage gracefully', async () => {
    chatState.update(s => ({ ...s, initialized: false }))
    sessionStorage.setItem('dk_chat_session', 'not-json')

    await initChat([{ name: 'default' }])
    const state = get(chatState)
    expect(state.initialized).toBe(true)
    expect(state.restoring).toBe(false)
  })

  test('handles empty history from restored session', async () => {
    chatState.update(s => ({ ...s, initialized: false }))
    sessionStorage.setItem('dk_chat_session', JSON.stringify({ sessionId: 'sess-empty', agent: 'default' }))
    mockSessionMessages.mockResolvedValue([])

    await initChat([{ name: 'default' }])
    const state = get(chatState)
    // Should not set sessionId when history is empty
    expect(state.messages).toHaveLength(0)
    expect(state.initialized).toBe(true)
  })

  test('handles API error during restore', async () => {
    chatState.update(s => ({ ...s, initialized: false }))
    sessionStorage.setItem('dk_chat_session', JSON.stringify({ sessionId: 'sess-fail', agent: 'default' }))
    mockSessionMessages.mockRejectedValue(new Error('server down'))

    await initChat([{ name: 'default' }])
    const state = get(chatState)
    expect(state.initialized).toBe(true)
    expect(state.restoring).toBe(false)
  })
})

describe('resolveApprovalAction', () => {
  const approval = { id: 'appr-1', tool: 'web_search', text: 'search', status: 'pending' }

  test('WS path: sends approve action', async () => {
    mockWsStatus.set('connected')
    await resolveApprovalAction(approval, true)
    expect(mockSend).toHaveBeenCalledWith({
      type: 'approval_response',
      approval_id: 'appr-1',
      action: 'approve',
    })
  })

  test('WS path: sends deny action', async () => {
    mockWsStatus.set('connected')
    await resolveApprovalAction(approval, false)
    expect(mockSend).toHaveBeenCalledWith({
      type: 'approval_response',
      approval_id: 'appr-1',
      action: 'deny',
    })
  })

  test('WS path: sends auto_session action', async () => {
    mockWsStatus.set('connected')
    await resolveApprovalAction(approval, true, 'session')
    expect(mockSend).toHaveBeenCalledWith({
      type: 'approval_response',
      approval_id: 'appr-1',
      action: 'auto_session',
    })
  })

  test('WS path: sends auto_always action', async () => {
    mockWsStatus.set('connected')
    await resolveApprovalAction(approval, true, 'permanent')
    expect(mockSend).toHaveBeenCalledWith({
      type: 'approval_response',
      approval_id: 'appr-1',
      action: 'auto_always',
    })
  })

  test('REST fallback: calls api.approveApproval', async () => {
    mockWsStatus.set('disconnected')
    mockApproveApproval.mockResolvedValue({ ok: true })
    await resolveApprovalAction(approval, true, 'session')
    expect(mockApproveApproval).toHaveBeenCalledWith('appr-1', 'session')
  })

  test('REST fallback: calls api.denyApproval', async () => {
    mockWsStatus.set('disconnected')
    mockDenyApproval.mockResolvedValue({ ok: true })
    await resolveApprovalAction(approval, false)
    expect(mockDenyApproval).toHaveBeenCalledWith('appr-1')
  })

  test('REST fallback: approve without autoApproveScope passes undefined', async () => {
    mockWsStatus.set('disconnected')
    mockApproveApproval.mockResolvedValue({ ok: true })
    await resolveApprovalAction(approval, true)
    expect(mockApproveApproval).toHaveBeenCalledWith('appr-1', undefined)
  })
})

describe('sendViaWS edge cases', () => {
  test('rejects when client.send returns false (not connected)', async () => {
    mockWsStatus.set('connected')
    mockSend.mockReturnValue(false)

    await sendMessage('hello')
    const state = get(chatState)
    expect(state.error).toBe('WebSocket not connected')
    expect(state.sending).toBe(false)
  })

  test('re-registers handler when server assigns new session_id', async () => {
    mockWsStatus.set('connected')

    mockOnSessionEvent.mockImplementation((id, handler) => {
      // Simulate server responding with a NEW session_id
      setTimeout(() => {
        handler({ type: 'content', text: 'reply', session_id: 'server-assigned-id' })
        handler({ type: 'done', session_id: 'server-assigned-id' })
      }, 0)
    })

    await sendMessage('hello')
    // offSessionEvent should have been called for the original registration
    // and onSessionEvent called again for the new session_id
    const offCalls = mockOffSessionEvent.mock.calls.map(c => c[0])
    expect(offCalls).toContain('__pending__')
    expect(get(chatState).sessionId).toBe('server-assigned-id')
  })

  test('handles error frame from WS', async () => {
    mockWsStatus.set('connected')

    mockOnSessionEvent.mockImplementation((id, handler) => {
      setTimeout(() => {
        handler({ type: 'error', message: 'server exploded', session_id: id })
      }, 0)
    })

    await sendMessage('hello')
    const state = get(chatState)
    expect(state.error).toBe('server exploded')
    expect(state.sending).toBe(false)
  })

  test('WS tool events are routed through handleToolEvent', async () => {
    mockWsStatus.set('connected')

    mockOnSessionEvent.mockImplementation((id, handler) => {
      setTimeout(() => {
        handler({ type: 'tool_start', tool: 'fetch', round: 1, session_id: id })
        handler({ type: 'tool_end', tool: 'fetch', round: 1, duration_ms: 200, session_id: id })
        handler({ type: 'content', text: 'done', session_id: id })
        handler({ type: 'done', session_id: id })
      }, 0)
    })

    await sendMessage('fetch data')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.toolCalls).toHaveLength(1)
    expect(agentMsg.toolCalls[0].name).toBe('fetch')
    expect(agentMsg.toolCalls[0].status).toBe('done')
    expect(agentMsg.toolCalls[0].duration).toBe(200)
  })
})

describe('handleToolEvent: tool_start marks pending approvals as approved', () => {
  test('pending approval for same tool is marked approved on tool_start', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_approval', approval_id: 'a1', tool: 'web_search', text: 'search', approval_status: 'pending' })
      onToolEvent({ type: 'tool_start', tool: 'web_search', round: 1 })
      onDone('sess-1')
    })

    await sendMessage('search')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.approvals[0].status).toBe('approved')
    expect(agentMsg.toolCalls).toHaveLength(1)
  })

  test('pending approval for different tool is not affected by tool_start', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_approval', approval_id: 'a1', tool: 'web_search', text: 'search', approval_status: 'pending' })
      onToolEvent({ type: 'tool_start', tool: 'different_tool', round: 1 })
      onDone('sess-1')
    })

    await sendMessage('search')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.approvals[0].status).toBe('pending')
  })
})

describe('handleToolEvent: tool_id-based matching', () => {
  test('tool_end matches by tool_id when available', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_start', tool: 'web_fetch', tool_id: 'call_1', round: 1 })
      onToolEvent({ type: 'tool_start', tool: 'web_fetch', tool_id: 'call_2', round: 1 })
      onToolEvent({ type: 'tool_end', tool: 'web_fetch', tool_id: 'call_2', round: 1, duration_ms: 100 })
      onToolEvent({ type: 'tool_end', tool: 'web_fetch', tool_id: 'call_1', round: 1, duration_ms: 300 })
      onDone('sess-1')
    })

    await sendMessage('fetch two pages')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.toolCalls).toHaveLength(2)
    expect(agentMsg.toolCalls[0].id).toBe('call_1')
    expect(agentMsg.toolCalls[0].status).toBe('done')
    expect(agentMsg.toolCalls[0].duration).toBe(300)
    expect(agentMsg.toolCalls[1].id).toBe('call_2')
    expect(agentMsg.toolCalls[1].status).toBe('done')
    expect(agentMsg.toolCalls[1].duration).toBe(100)
  })

  test('tool_end falls back to name+round when tool_id is absent', async () => {
    mockStreamChat.mockImplementation(async (agent, sid, msg, onChunk, onDone, onToolEvent) => {
      onToolEvent({ type: 'tool_start', tool: 'web_search', round: 1 })
      onToolEvent({ type: 'tool_end', tool: 'web_search', round: 1, duration_ms: 200 })
      onDone('sess-1')
    })

    await sendMessage('search')
    const agentMsg = get(chatState).messages[1]
    expect(agentMsg.toolCalls[0].status).toBe('done')
    expect(agentMsg.toolCalls[0].duration).toBe(200)
  })
})

describe('pendingSkillTest', () => {
  test('starts as null', () => {
    expect(get(pendingSkillTest)).toBeNull()
  })

  test('can be set and cleared', () => {
    pendingSkillTest.set({ agent: 'helper', command: '/briefing' })
    expect(get(pendingSkillTest)).toEqual({ agent: 'helper', command: '/briefing' })

    pendingSkillTest.set(null)
    expect(get(pendingSkillTest)).toBeNull()
  })
})
