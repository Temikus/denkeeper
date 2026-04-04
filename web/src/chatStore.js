import { writable, get } from 'svelte/store'
import { api } from './api.js'

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

// Module-level reference to the in-flight agent message so SSE callbacks
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

export async function sendMessage(text) {
  text = text.trim()
  const state = get(chatState)
  if (!text || state.sending) return

  const agentMsg = {
    role: 'agent', text: '', streaming: true,
    toolCalls: [], status: '', tokens: 0, costUSD: 0,
  }
  activeAgentMsg = agentMsg

  chatState.update(s => ({
    ...s,
    sending: true,
    error: '',
    messages: [...s.messages, { role: 'user', text }, agentMsg],
  }))

  try {
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
      (evt) => {
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
        if (evt.type === 'tool_start') {
          agentMsg.status = ''
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
    )
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
