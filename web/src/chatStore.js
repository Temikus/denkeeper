import { writable, get } from 'svelte/store'
import { api } from './api.js'
import { wsStatus, getWSClient, onSessionEvent, offSessionEvent } from './wsStore.js'

const STORAGE_KEY = 'dk_chat_session'

export const chatState = writable({
  messages: [],
  sessionId: '',
  agent: 'default',
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
    agentMsg.toolCalls = [...agentMsg.toolCalls, { name: evt.tool, round: evt.round, status: 'running' }]
  }
  if (evt.type === 'tool_end') {
    const tc = agentMsg.toolCalls.find(t => t.name === evt.tool && t.round === evt.round)
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
        agentMsg.text += frame.text || ''
        touchMessages()
      } else if (frame.type === 'done') {
        agentMsg.streaming = false
        agentMsg.status = ''
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
    role: 'agent', text: '', streaming: true,
    toolCalls: [], approvals: [], status: '', tokens: 0, costUSD: 0,
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
          chatState.update(s => ({ ...s, sessionId: doneSessionId }))
          saveSession()
          touchMessages()
        },
        (evt) => handleToolEvent(agentMsg, evt)
      )
    }
  } catch (e) {
    agentMsg.text = '\u26a0 ' + e.message
    agentMsg.streaming = false
    chatState.update(s => ({ ...s, error: e.message }))
    touchMessages()
  } finally {
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
    client.send({
      type: 'approval_response',
      approval_id: appr.id,
      action,
    })
  } else {
    // REST fallback.
    if (approve) {
      await api.approveApproval(appr.id, autoApproveScope || undefined)
    } else {
      await api.denyApproval(appr.id)
    }
  }
}
