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
}
