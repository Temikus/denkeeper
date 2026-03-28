import { get } from 'svelte/store'
import { token } from './store.js'

function authHeaders() {
  return {
    'Authorization': `Bearer ${get(token)}`,
    'Content-Type': 'application/json',
  }
}

async function apiFetch(path, options = {}) {
  const res = await fetch(path, {
    ...options,
    headers: { ...authHeaders(), ...(options.headers || {}) },
  })

  if (res.status === 401) {
    token.clear()
    throw new Error('Unauthorized — please log in again')
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `HTTP ${res.status}`)
  }
  // 204 No Content has no body.
  if (res.status === 204) return null
  return res.json()
}

export const api = {
  // Health (no auth needed, but we send it anyway — harmless).
  health: () => fetch('/api/v1/health').then(r => r.json()),

  // Agents
  agents: () => apiFetch('/api/v1/agents'),
  agent: name => apiFetch(`/api/v1/agents/${encodeURIComponent(name)}`),

  // Costs
  costs: () => apiFetch('/api/v1/costs'),

  // Skills
  skills: () => apiFetch('/api/v1/skills'),
  skillsByAgent: agent => apiFetch(`/api/v1/skills/${encodeURIComponent(agent)}`),

  // Schedules
  schedules: () => apiFetch('/api/v1/schedules'),

  // Sessions
  sessions: () => apiFetch('/api/v1/sessions'),
  sessionMessages: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}/messages`),
  deleteSession: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Approvals
  approvals: (status = '') => apiFetch(`/api/v1/approvals${status ? `?status=${encodeURIComponent(status)}` : ''}`),
  approval: id => apiFetch(`/api/v1/approvals/${encodeURIComponent(id)}`),
  approveApproval: id => apiFetch(`/api/v1/approvals/${encodeURIComponent(id)}/approve`, { method: 'POST' }),
  denyApproval: id => apiFetch(`/api/v1/approvals/${encodeURIComponent(id)}/deny`, { method: 'POST' }),

  // Setup (no auth required — only usable when no keys exist)
  setupStatus: () => fetch('/api/v1/setup').then(r => r.json()),
  setupInit: (name, scopes) => fetch('/api/v1/setup', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, scopes }),
  }).then(async r => {
    if (!r.ok) {
      const body = await r.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${r.status}`)
    }
    return r.json()
  }),

  // Tools & Plugins
  listTools: () => apiFetch('/api/v1/tools'),
  getTool: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}`),
  addTool: (name, command, args, env) => apiFetch('/api/v1/tools', {
    method: 'POST',
    body: JSON.stringify({ name, command, args: args || [], env: env || {} }),
  }),
  removeTool: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  listPlugins: () => apiFetch('/api/v1/plugins'),
  getPlugin: name => apiFetch(`/api/v1/plugins/${encodeURIComponent(name)}`),
  addPlugin: cfg => apiFetch('/api/v1/plugins', {
    method: 'POST',
    body: JSON.stringify(cfg),
  }),
  removePlugin: name => apiFetch(`/api/v1/plugins/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // API Keys
  listKeys: () => apiFetch('/api/v1/keys'),
  createKey: (name, scopes) => apiFetch('/api/v1/keys', {
    method: 'POST',
    body: JSON.stringify({ name, scopes }),
  }),
  revokeKey: id => apiFetch(`/api/v1/keys/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  rotateKey: id => apiFetch(`/api/v1/keys/${encodeURIComponent(id)}/rotate`, { method: 'POST' }),
  deleteKey: id => apiFetch(`/api/v1/keys/${encodeURIComponent(id)}/permanent`, { method: 'DELETE' }),

  // Chat with SSE streaming.
  // onChunk(text) is called for each content chunk.
  // onDone(sessionId) is called when the stream ends.
  streamChat: async (agent, sessionId, message, onChunk, onDone) => {
    const res = await fetch('/api/v1/chat', {
      method: 'POST',
      headers: {
        ...authHeaders(),
        'Accept': 'text/event-stream',
      },
      body: JSON.stringify({ agent, session_id: sessionId || undefined, message }),
    })
    if (res.status === 401) {
      token.clear()
      throw new Error('Unauthorized — please log in again')
    }
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${res.status}`)
    }
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      // SSE frames are separated by "\n\n".
      const frames = buf.split('\n\n')
      buf = frames.pop() // keep incomplete frame
      for (const frame of frames) {
        const line = frame.trim()
        if (!line.startsWith('data: ')) continue
        try {
          const evt = JSON.parse(line.slice(6))
          if (evt.type === 'content') onChunk(evt.text || '')
          if (evt.type === 'done') onDone(evt.session_id || '')
          if (evt.type === 'error') throw new Error(evt.message || 'stream error')
        } catch (e) {
          if (e.message !== 'stream error') continue // skip malformed JSON
          throw e
        }
      }
    }
  },
}
