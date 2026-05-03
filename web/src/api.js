import { get } from 'svelte/store'
import { token, authMode } from './store.js'

function authHeaders() {
  // When using session cookies, no Authorization header needed.
  if (get(authMode) === 'session') {
    return { 'Content-Type': 'application/json' }
  }
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
    // The auth middleware sets X-Auth-Failure when the credential itself
    // is bad (missing/revoked key, expired session). Without that header
    // a 401 came from somewhere inside the handler and shouldn't nuke
    // the user's credential — surface the error and let the caller cope.
    if (res.headers.get('X-Auth-Failure') === 'credential-invalid') {
      token.clear()
      authMode.set(null)
      throw new Error('Unauthorized — please log in again')
    }
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || 'Unauthorized')
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
  models: () => apiFetch('/api/v1/models').then(r => r.models || []).catch(() => []),
  modelDetails: (provider) => apiFetch(`/api/v1/models/details${provider ? `?provider=${encodeURIComponent(provider)}` : ''}`).then(r => r.models || []).catch(() => []),
  agent: name => apiFetch(`/api/v1/agents/${encodeURIComponent(name)}`),

  // Agent config mutation
  updateAgentConfig: (name, data) => apiFetch(`/api/v1/agents/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  }),
  createAgent: (data) => apiFetch('/api/v1/agents', {
    method: 'POST',
    body: JSON.stringify(data),
  }),
  deleteAgent: (name) => apiFetch(`/api/v1/agents/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // Costs
  costs: (agent) => apiFetch(`/api/v1/costs${agent ? `?agent=${encodeURIComponent(agent)}` : ''}`),

  // Skills
  skills: () => apiFetch('/api/v1/skills'),
  skillsByAgent: agent => apiFetch(`/api/v1/skills/${encodeURIComponent(agent)}`),
  getSkill: (agent, name) => apiFetch(`/api/v1/skills/${encodeURIComponent(agent)}/${encodeURIComponent(name)}`),
  addSkill: (agent, data) => apiFetch(`/api/v1/skills/${encodeURIComponent(agent)}`, {
    method: 'POST',
    body: JSON.stringify(data),
  }),
  updateSkill: (agent, name, data) => apiFetch(`/api/v1/skills/${encodeURIComponent(agent)}/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  }),
  deleteSkill: (agent, name) => apiFetch(`/api/v1/skills/${encodeURIComponent(agent)}/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // Schedules
  schedules: () => apiFetch('/api/v1/schedules'),
  addSchedule: data => apiFetch('/api/v1/schedules', {
    method: 'POST',
    body: JSON.stringify(data),
  }),
  updateSchedule: (name, data) => apiFetch(`/api/v1/schedules/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  }),
  deleteSchedule: name => apiFetch(`/api/v1/schedules/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // Channels
  channels: () => apiFetch('/api/v1/channels'),
  channel: (name) => apiFetch(`/api/v1/channels/${encodeURIComponent(name)}`),
  activateChannel: (name, adapterKey) => apiFetch(`/api/v1/channels/${encodeURIComponent(name)}/activate`, {
    method: 'POST',
    body: JSON.stringify({ adapter_key: adapterKey }),
  }),
  deactivateChannel: (name, adapterKey) => apiFetch(`/api/v1/channels/${encodeURIComponent(name)}/activate`, {
    method: 'DELETE',
    body: JSON.stringify({ adapter_key: adapterKey }),
  }),
  createChannel: (data) => apiFetch('/api/v1/channels', {
    method: 'POST',
    body: JSON.stringify(data),
  }),
  updateChannel: (name, data) => apiFetch(`/api/v1/channels/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  }),
  deleteChannel: (name) => apiFetch(`/api/v1/channels/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // Sessions
  sessions: (opts = {}) => {
    const params = new URLSearchParams()
    if (opts.limit) params.set('limit', String(opts.limit))
    if (opts.offset) params.set('offset', String(opts.offset))
    if (opts.agent) params.set('agent', opts.agent)
    const qs = params.toString()
    return apiFetch(`/api/v1/sessions${qs ? '?' + qs : ''}`)
  },
  sessionMessages: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}/messages`),
  deleteSession: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  clearSession: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}/clear`, { method: 'POST' }),
  compactSession: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}/compact`, { method: 'POST' }),
  sessionStats: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}/stats`),
  sessionToolCalls: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}/tool-calls`),
  sessionSkills: id => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}/skills`),

  // Approvals
  approvals: (status = '') => apiFetch(`/api/v1/approvals${status ? `?status=${encodeURIComponent(status)}` : ''}`),
  approval: id => apiFetch(`/api/v1/approvals/${encodeURIComponent(id)}`),
  approveApproval: (id, autoApprove) => {
    const qs = autoApprove ? `?auto_approve=${encodeURIComponent(autoApprove)}` : ''
    return apiFetch(`/api/v1/approvals/${encodeURIComponent(id)}/approve${qs}`, { method: 'POST' })
  },
  denyApproval: id => apiFetch(`/api/v1/approvals/${encodeURIComponent(id)}/deny`, { method: 'POST' }),

  // Auto-approve rules
  listAutoApprove: (agent) => apiFetch(`/api/v1/auto-approve${agent ? `?agent=${encodeURIComponent(agent)}` : ''}`),
  createAutoApprove: (rule) => apiFetch('/api/v1/auto-approve', {
    method: 'POST',
    body: JSON.stringify(rule),
  }),
  deleteAutoApprove: id => apiFetch(`/api/v1/auto-approve/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  // Setup (no auth required — only usable when no keys exist)
  setupStatus: () => fetch('/api/v1/setup').then(r => r.json()),
  setupAccount: (pin, password) => fetch('/api/v1/setup/account', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ pin, password }),
  }).then(async r => {
    if (!r.ok) {
      const body = await r.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${r.status}`)
    }
    return r.json()
  }),
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
  addTool: (cfg) => apiFetch('/api/v1/tools', {
    method: 'POST',
    body: JSON.stringify(cfg),
  }),
  updateTool: (name, cfg) => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(cfg),
  }),
  removeTool: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  restartTool: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}/restart`, { method: 'POST' }),
  toolHealth: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}/health`),
  toolDefs: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}/defs`),

  // OAuth tool endpoints
  toolOAuthStatus: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}/oauth`),
  toolOAuthConnect: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}/oauth/connect`, { method: 'POST' }),
  toolOAuthRevoke: name => apiFetch(`/api/v1/tools/${encodeURIComponent(name)}/oauth/token`, { method: 'DELETE' }),
  listPendingOAuth: () => apiFetch('/api/v1/tools/oauth/pending'),
  listPlugins: () => apiFetch('/api/v1/plugins'),
  getPlugin: name => apiFetch(`/api/v1/plugins/${encodeURIComponent(name)}`),
  addPlugin: cfg => apiFetch('/api/v1/plugins', {
    method: 'POST',
    body: JSON.stringify(cfg),
  }),
  removePlugin: name => apiFetch(`/api/v1/plugins/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  // Persona
  getPersona: (agent, section) => apiFetch(`/api/v1/agents/${encodeURIComponent(agent)}/persona/${encodeURIComponent(section)}`),
  updatePersona: (agent, section, content) => apiFetch(`/api/v1/agents/${encodeURIComponent(agent)}/persona/${encodeURIComponent(section)}`, {
    method: 'PUT',
    body: JSON.stringify({ content }),
  }),

  // KV Store
  kvList: (agent, prefix) => {
    const params = prefix ? `?prefix=${encodeURIComponent(prefix)}` : ''
    return apiFetch(`/api/v1/kv/${encodeURIComponent(agent)}${params}`)
  },
  kvGet: (agent, key) => apiFetch(`/api/v1/kv/${encodeURIComponent(agent)}/${encodeURIComponent(key)}`),
  kvSet: (agent, key, value, ttl) => apiFetch(`/api/v1/kv/${encodeURIComponent(agent)}/${encodeURIComponent(key)}`, {
    method: 'PUT',
    body: JSON.stringify({ value, ttl: ttl || undefined }),
  }),
  kvDelete: (agent, key) => apiFetch(`/api/v1/kv/${encodeURIComponent(agent)}/${encodeURIComponent(key)}`, { method: 'DELETE' }),

  // Browser
  browserProfiles: () => apiFetch('/api/v1/browser/profiles'),
  browserProfile: name => apiFetch(`/api/v1/browser/profiles/${encodeURIComponent(name)}`),
  deleteBrowserProfile: name => apiFetch(`/api/v1/browser/profiles/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  browserSessions: () => apiFetch('/api/v1/browser/sessions'),
  browserConfig: () => apiFetch('/api/v1/browser/config'),

  // LLM providers
  llmProviders: () => apiFetch('/api/v1/llm/providers'),
  createLLMProvider: (data) => apiFetch('/api/v1/llm/providers', {
    method: 'POST',
    body: JSON.stringify(data),
  }),
  updateLLMProvider: (name, data) => apiFetch(`/api/v1/llm/providers/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify(data),
  }),
  deleteLLMProvider: (name) => apiFetch(`/api/v1/llm/providers/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  updateLLMConfig: (data) => apiFetch('/api/v1/llm/config', {
    method: 'PATCH',
    body: JSON.stringify(data),
  }),

  // Server config
  serverConfig: () => apiFetch('/api/v1/server/config'),
  updateServerConfig: (data) => apiFetch('/api/v1/server/config', {
    method: 'PATCH',
    body: JSON.stringify(data),
  }),
  reloadConfig: () => apiFetch('/api/v1/server/reload', { method: 'POST' }),
  restartProcess: () => apiFetch('/api/v1/server/restart', { method: 'POST' }),

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
  // onToolEvent(evt) is called for tool_start/tool_end events.
  streamChat: async (agent, sessionId, message, onChunk, onDone, onToolEvent, channel) => {
    const res = await fetch('/api/v1/chat', {
      method: 'POST',
      headers: {
        ...authHeaders(),
        'Accept': 'text/event-stream',
      },
      body: JSON.stringify({ agent, session_id: sessionId || undefined, channel: channel || undefined, message }),
    })
    if (res.status === 401) {
      if (res.headers.get('X-Auth-Failure') === 'credential-invalid') {
        token.clear()
        authMode.set(null)
        throw new Error('Unauthorized — please log in again')
      }
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || 'Unauthorized')
    }
    if (!res.ok) {
      const body = await res.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${res.status}`)
    }
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    let gotDone = false
    try {
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
          let evt
          try {
            evt = JSON.parse(line.slice(6))
          } catch (_) {
            continue // skip malformed JSON
          }
          if (evt.type === 'content') onChunk(evt.text || '')
          if (evt.type === 'done') { gotDone = true; onDone(evt.session_id || '') }
          if (evt.type === 'thinking' || evt.type === 'usage' || evt.type === 'tool_start' || evt.type === 'tool_end' || evt.type === 'tool_approval' || evt.type === 'cost_limit' || evt.type === 'content_delta' || evt.type === 'thinking_delta') onToolEvent?.(evt)
          if (evt.type === 'error') throw new Error(evt.message || 'stream error')
        }
      }
      // Stream closed without a done event — signal completion anyway so the UI
      // doesn't stay stuck in a "streaming" state forever.
      if (!gotDone) onDone('')
    } finally {
      reader.cancel().catch(() => {})
    }
  },

  // Auth management (requires auth).
  authStatus: () => apiFetch('/api/v1/auth/status'),
  listAuthSessions: () => apiFetch('/api/v1/auth/sessions'),
  revokeAuthSession: (id) => apiFetch(`/api/v1/auth/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  revokeAllAuthSessions: () => apiFetch('/api/v1/auth/sessions', { method: 'DELETE' }),
  changePassword: (currentPassword, newPassword) => apiFetch('/api/v1/auth/password', {
    method: 'POST',
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
  }),
  testOIDC: () => apiFetch('/api/v1/auth/oidc/test'),
  updateAuthPreferences: (method) => apiFetch('/api/v1/auth/preferences', {
    method: 'POST',
    body: JSON.stringify({ preferred_login_method: method }),
  }),

  // Onboarding.
  onboarding: () => apiFetch('/api/v1/onboarding'),
  dismissOnboarding: () => apiFetch('/api/v1/onboarding/dismiss', { method: 'POST' }),
  wizardComplete: () => apiFetch('/api/v1/onboarding/wizard-complete', { method: 'POST' }),

  // Auth endpoints (no auth required).
  authConfig: () => fetch('/auth/config').then(r => r.json()),
  passwordLogin: (password) => fetch('/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
  }).then(async r => {
    if (!r.ok) {
      const body = await r.json().catch(() => ({}))
      throw new Error(body.error || `HTTP ${r.status}`)
    }
    return r.json()
  }),
  // Returns the raw Response for status code / header inspection.
  rawPasswordLogin: (password) => fetch('/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
  }),
  logout: () => fetch('/auth/logout', { method: 'POST' }).then(r => r.json()),
  sessionCheck: () => fetch('/auth/session').then(r => r.json()),

  // Audit Log
  auditEvents: (params = {}) => {
    const filtered = Object.fromEntries(Object.entries(params).filter(([, v]) => v))
    return apiFetch(`/api/v1/audit?${new URLSearchParams(filtered)}`)
  },
  auditStats: (since) => apiFetch(`/api/v1/audit/stats${since ? `?since=${encodeURIComponent(since)}` : ''}`),

  // Safety — stop/panic/resume.
  stopSession: (id) => apiFetch(`/api/v1/sessions/${encodeURIComponent(id)}/stop`, { method: 'POST' }),
  panic: () => apiFetch('/api/v1/panic', { method: 'POST' }),
  resume: () => apiFetch('/api/v1/resume', { method: 'POST' }),
  panicStatus: () => apiFetch('/api/v1/panic'),
}
