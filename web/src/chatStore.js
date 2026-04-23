import { writable, get } from 'svelte/store'
import { api } from './api.js'
import { wsStatus, getWSClient, onSessionEvent, offSessionEvent } from './wsStore.js'

const STORAGE_KEY = 'dk_chat_session'

// Set by Skills page to queue a test run, consumed by Chat on mount.
export const pendingSkillTest = writable(null) // { agent: string, command: string }

export const chatState = writable({
  messages: [],
  sessionId: '',
  agent: 'default',
  channel: '',
  sending: false,
  error: '',
  restoring: false,
  initialized: false,
})

// Module-level reference to the in-flight agent message so callbacks
// can keep updating it even after the Chat component unmounts.
let activeAgentMsg = null

// Notify all subscribers that messages changed (creates new array ref).
function touchMessages() {
  chatState.update(s => ({ ...s, messages: [...s.messages] }))
}

// Throttled version for high-frequency streaming deltas (~60fps).
let _throttleTimer = null
function touchMessagesThrottled() {
  if (_throttleTimer) return
  _throttleTimer = setTimeout(() => {
    _throttleTimer = null
    touchMessages()
  }, 16)
}
// Flush any pending throttled update immediately.
function flushThrottledTouch() {
  if (_throttleTimer) {
    clearTimeout(_throttleTimer)
    _throttleTimer = null
    touchMessages()
  }
}

function saveSession() {
  const { sessionId, agent } = get(chatState)
  if (!sessionId) return
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ sessionId, agent }))
  } catch (_) { /* quota exceeded */ }
}

export function newSession() {
  activeAgentMsg = null
  chatState.update(s => ({ ...s, sessionId: '', messages: [], error: '' }))
  sessionStorage.removeItem(STORAGE_KEY)
}

export function setAgent(name) {
  chatState.update(s => ({ ...s, agent: name }))
}

export function setChannel(name) {
  chatState.update(s => ({ ...s, channel: name }))
}

export async function loadSession(sessionId, agent) {
  if (!sessionId) return
  chatState.update(s => ({ ...s, restoring: true, error: '' }))
  try {
    const history = await api.sessionMessages(sessionId)
    chatState.update(s => ({
      ...s,
      sessionId,
      agent: agent || s.agent,
      messages: (history || []).map(m => ({
        role: m.role === 'assistant' ? 'agent' : 'user',
        text: m.content,
      })),
      restoring: false,
    }))
    saveSession()
  } catch (e) {
    chatState.update(s => ({ ...s, restoring: false, error: 'Failed to load session' }))
  }
}

export async function initChat(agents) {
  const state = get(chatState)
  if (state.initialized) return
  if (agents.length > 0 && !state.sessionId) {
    chatState.update(s => ({ ...s, agent: agents[0].name }))
  }
  await restoreSession()
  chatState.update(s => ({ ...s, initialized: true }))
}

async function restoreSession() {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return
    const saved = JSON.parse(raw)
    if (!saved.sessionId) return

    chatState.update(s => ({ ...s, restoring: true }))
    const history = await api.sessionMessages(saved.sessionId)
    if (!history || history.length === 0) {
      chatState.update(s => ({ ...s, restoring: false }))
      return
    }

    chatState.update(s => ({
      ...s,
      sessionId: saved.sessionId,
      agent: saved.agent || s.agent,
      messages: history.map(m => ({
        role: m.role === 'assistant' ? 'agent' : 'user',
        text: m.content,
      })),
      restoring: false,
    }))
  } catch (_) {
    chatState.update(s => ({ ...s, restoring: false }))
  }
}

// --- Shared event handler for both WS and SSE ---

function handleToolEvent(agentMsg, evt) {
  if (evt.type === 'content_delta') {
    agentMsg.text += evt.text || ''
    agentMsg.status = ''
    agentMsg._hadDeltas = true
    touchMessagesThrottled()
    return
  }
  if (evt.type === 'thinking_delta') {
    agentMsg.thinking = (agentMsg.thinking || '') + (evt.text || '')
    touchMessagesThrottled()
    return
  }
  if (evt.type === 'thinking') {
    agentMsg.status = evt.text || 'Thinking...'
    touchMessages()
    return
  }
  if (evt.type === 'usage') {
    agentMsg.tokens = evt.tokens
    agentMsg.costUSD = evt.cost_usd
    touchMessages()
    return
  }
  if (evt.type === 'cost_limit') {
    agentMsg.costWarning = evt.text || 'Session cost limit reached — tool use paused.'
    touchMessages()
    return
  }
  if (evt.type === 'tool_approval') {
    if (evt.approval_status === 'auto_approved') {
      agentMsg.approvals = [...agentMsg.approvals, {
        id: evt.approval_id, tool: evt.tool, text: evt.text,
        status: 'auto_approved', resolving: false,
      }]
    } else {
      agentMsg.status = `Waiting for approval: ${evt.tool}`
      agentMsg.approvals = [...agentMsg.approvals, {
        id: evt.approval_id, tool: evt.tool, text: evt.text,
        status: 'pending', resolving: false,
      }]
    }
  }
  if (evt.type === 'tool_start') {
    agentMsg.status = ''
    const pendingAppr = agentMsg.approvals.find(a => a.tool === evt.tool && a.status === 'pending')
    if (pendingAppr) {
      pendingAppr.status = 'approved'
      agentMsg.approvals = [...agentMsg.approvals]
    }
    // Deduplicate only when tool_id is present (replay protection).
    // Without tool_id, name+round can't distinguish replays from legitimate
    // second invocations of the same tool, so always append.
    const isDupe = evt.tool_id && agentMsg.toolCalls.some(t => t.id === evt.tool_id)
    if (!isDupe) {
      agentMsg.toolCalls = [...agentMsg.toolCalls, { id: evt.tool_id, name: evt.tool, round: evt.round, status: 'running' }]
    }
  }
  if (evt.type === 'tool_end') {
    const tc = evt.tool_id
      ? agentMsg.toolCalls.find(t => t.id === evt.tool_id)
      : agentMsg.toolCalls.find(t => t.name === evt.tool && t.round === evt.round)
    if (tc) {
      tc.status = evt.error ? 'error' : 'done'
      tc.duration = evt.duration_ms
      tc.error = evt.error
    }
    agentMsg.toolCalls = [...agentMsg.toolCalls]
  }
  touchMessages()
}

// --- WebSocket-based send ---

function sendViaWS(agentMsg, agentName, sessionId, text) {
  return new Promise((resolve, reject) => {
    const client = getWSClient()

    // Generate a temporary session ID if none exists; the server will
    // assign the real one in the done frame.
    const reqSessionId = sessionId || ''

    const handler = (frame) => {
      if (frame.type === 'content') {
        // If deltas were already streamed, use the final content as the
        // authoritative text (replaces accumulated deltas). Otherwise append.
        if (agentMsg._hadDeltas) {
          agentMsg.text = frame.text || agentMsg.text
        } else {
          agentMsg.text += frame.text || ''
        }
        touchMessages()
      } else if (frame.type === 'done') {
        agentMsg.streaming = false
        agentMsg.status = ''
        // Finalize any tool calls still stuck in "running" (e.g. missed tool_end)
        for (const tc of agentMsg.toolCalls) {
          if (tc.status === 'running') tc.status = 'done'
        }
        agentMsg.toolCalls = [...agentMsg.toolCalls]
        flushThrottledTouch()
        const doneSessionId = frame.session_id || reqSessionId
        chatState.update(s => ({ ...s, sessionId: doneSessionId }))
        saveSession()
        touchMessages()
        offSessionEvent(doneSessionId)
        resolve()
      } else if (frame.type === 'error') {
        offSessionEvent(frame.session_id || reqSessionId)
        reject(new Error(frame.message || 'WebSocket stream error'))
      } else {
        handleToolEvent(agentMsg, frame)
      }
    }

    // We need to register the handler before sending. Since the server may
    // assign a new session_id, we register on the requested one first and
    // re-register when we see the real session_id in the first frame.
    let registeredId = reqSessionId || '__pending__'
    let realSessionId = null

    const wrappedHandler = (frame) => {
      // If we discover the real session_id, re-register.
      if (!realSessionId && frame.session_id && frame.session_id !== registeredId) {
        realSessionId = frame.session_id
        offSessionEvent(registeredId)
        registeredId = realSessionId
        onSessionEvent(registeredId, wrappedHandler)
      }
      handler(frame)
    }

    onSessionEvent(registeredId, wrappedHandler)

    const sent = client.send({
      type: 'chat_request',
      session_id: reqSessionId || undefined,
      agent: agentName,
      channel: get(chatState).channel || undefined,
      message: text,
    })

    if (!sent) {
      offSessionEvent(registeredId)
      reject(new Error('WebSocket not connected'))
    }
  })
}

// --- Main sendMessage (WS primary, SSE fallback) ---

export async function sendMessage(text) {
  text = text.trim()
  const state = get(chatState)
  if (!text || state.sending) return

  const agentMsg = {
    role: 'agent', text: '', thinking: '', streaming: true,
    toolCalls: [], approvals: [], status: '', tokens: 0, costUSD: 0,
    _hadDeltas: false, // tracks whether content_delta events were received
  }
  activeAgentMsg = agentMsg

  chatState.update(s => ({
    ...s,
    sending: true,
    error: '',
    messages: [...s.messages, { role: 'user', text }, agentMsg],
  }))

  try {
    const currentWSStatus = get(wsStatus)
    if (currentWSStatus === 'connected') {
      await sendViaWS(agentMsg, state.agent, state.sessionId, text)
    } else {
      // SSE fallback.
      await api.streamChat(state.agent, state.sessionId, text,
        (chunk) => {
          agentMsg.text += chunk
          touchMessages()
        },
        (doneSessionId) => {
          agentMsg.streaming = false
          agentMsg.status = ''
          for (const tc of agentMsg.toolCalls) {
            if (tc.status === 'running') tc.status = 'done'
          }
          agentMsg.toolCalls = [...agentMsg.toolCalls]
          flushThrottledTouch()
          chatState.update(s => ({ ...s, sessionId: doneSessionId }))
          saveSession()
          touchMessages()
        },
        (evt) => handleToolEvent(agentMsg, evt),
        state.channel
      )
    }
  } catch (e) {
    agentMsg.streaming = false
    // Don't overwrite partial streamed content with the error — show it
    // in the error bar only. If no content was received at all, show inline.
    if (!agentMsg.text) {
      agentMsg.text = '\u26a0 ' + e.message
    }
    chatState.update(s => ({ ...s, error: e.message }))
    touchMessages()
  } finally {
    // Ensure streaming flag is always cleared so the UI never gets stuck.
    if (agentMsg.streaming) {
      agentMsg.streaming = false
      agentMsg.status = ''
      touchMessages()
    }
    activeAgentMsg = null
    chatState.update(s => ({ ...s, sending: false }))
  }
}

/**
 * Resolve an inline approval. Uses WS when connected, REST otherwise.
 * @returns {Promise<void>}
 */
export async function resolveApprovalAction(appr, approve, autoApproveScope) {
  const currentWSStatus = get(wsStatus)
  if (currentWSStatus === 'connected') {
    const client = getWSClient()
    let action = approve ? 'approve' : 'deny'
    if (autoApproveScope === 'session') action = 'auto_session'
    if (autoApproveScope === 'permanent') action = 'auto_always'
    const sent = client.send({
      type: 'approval_response',
      approval_id: appr.id,
      action,
    })
    if (!sent) {
      throw new Error('WebSocket not connected — approval not sent')
    }
  } else {
    // REST fallback.
    if (approve) {
      await api.approveApproval(appr.id, autoApproveScope || undefined)
    } else {
      await api.denyApproval(appr.id)
    }
  }
}

/**
 * Cancel the in-flight request for the current session.
 * Uses WS cancel frame when connected, REST fallback otherwise.
 */
export async function cancelSession() {
  const state = get(chatState)
  if (!state.sending || !state.sessionId) return

  const currentWSStatus = get(wsStatus)
  if (currentWSStatus === 'connected') {
    const client = getWSClient()
    client.send({ type: 'cancel', session_id: state.sessionId })
  } else {
    await api.stopSession(state.sessionId).catch(() => {})
  }
}
