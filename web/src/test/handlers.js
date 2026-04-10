import { http, HttpResponse } from 'msw'
import {
  agents, sessions, messages, approvals, costs, skills, schedules,
  tools, plugins, browserProfiles, browserSessions, kvEntries,
  apiKeys, autoApproveRules, personaSections,
} from './fixtures/index.js'

export const handlers = [
  // Health
  http.get('/api/v1/health', () => HttpResponse.json({ status: 'ok' })),

  // Agents
  http.get('/api/v1/agents', () => HttpResponse.json(agents)),
  http.get('/api/v1/agents/:name', ({ params }) => {
    const agent = agents.find(a => a.name === params.name)
    return agent ? HttpResponse.json(agent) : new HttpResponse(null, { status: 404 })
  }),
  http.patch('/api/v1/agents/:name', () => HttpResponse.json({ ok: true })),

  // Models
  http.get('/api/v1/models', () => HttpResponse.json({ models: ['claude-3-opus', 'gpt-4o'] })),
  http.get('/api/v1/models/details', () => HttpResponse.json({ models: [
    { id: 'anthropic/claude-3-opus', name: 'Anthropic: Claude 3 Opus', provider: 'openrouter', input_per_mtok: 15.0, output_per_mtok: 75.0, supports_tools: true, weekly_tokens: 500000000 },
    { id: 'openai/gpt-4o', name: 'OpenAI: GPT-4o', provider: 'openrouter', input_per_mtok: 2.5, output_per_mtok: 10.0, supports_tools: true, weekly_tokens: 1000000000 },
    { id: 'meta-llama/llama-3.1-8b', name: 'Meta: Llama 3.1 8B', provider: 'openrouter', input_per_mtok: 0.05, output_per_mtok: 0.08, supports_tools: false, weekly_tokens: 200000000 },
    { id: 'google/gemma-2-9b:free', name: 'Google: Gemma 2 9B (free)', provider: 'openrouter', input_per_mtok: 0, output_per_mtok: 0, supports_tools: false, weekly_tokens: 100000000 },
  ] })),

  // Costs
  http.get('/api/v1/costs', () => HttpResponse.json(costs)),

  // Skills
  http.get('/api/v1/skills', () => HttpResponse.json(skills)),
  http.get('/api/v1/skills/:agent', ({ params }) =>
    HttpResponse.json(skills.filter(s => s.agent === params.agent))
  ),
  http.get('/api/v1/skills/:agent/:name', ({ params }) => {
    const skill = skills.find(s => s.agent === params.agent && s.name === params.name)
    return skill ? HttpResponse.json(skill) : new HttpResponse(null, { status: 404 })
  }),
  http.post('/api/v1/skills/:agent', () => HttpResponse.json({ ok: true })),
  http.put('/api/v1/skills/:agent/:name', () => HttpResponse.json({ ok: true })),
  http.delete('/api/v1/skills/:agent/:name', () => new HttpResponse(null, { status: 204 })),

  // Schedules
  http.get('/api/v1/schedules', () => HttpResponse.json(schedules)),
  http.post('/api/v1/schedules', () => HttpResponse.json({ ok: true })),
  http.patch('/api/v1/schedules/:name', () => HttpResponse.json({ ok: true })),
  http.delete('/api/v1/schedules/:name', () => new HttpResponse(null, { status: 204 })),

  // Sessions
  http.get('/api/v1/sessions', () => HttpResponse.json(sessions)),
  http.get('/api/v1/sessions/:id/messages', () => HttpResponse.json(messages)),
  http.delete('/api/v1/sessions/:id', () => new HttpResponse(null, { status: 204 })),

  // Approvals
  http.get('/api/v1/approvals', () => HttpResponse.json(approvals)),
  http.get('/api/v1/approvals/:id', ({ params }) => {
    const appr = approvals.find(a => a.id === params.id)
    return appr ? HttpResponse.json(appr) : new HttpResponse(null, { status: 404 })
  }),
  http.post('/api/v1/approvals/:id/approve', () => HttpResponse.json({ ok: true })),
  http.post('/api/v1/approvals/:id/deny', () => HttpResponse.json({ ok: true })),

  // Auto-approve rules
  http.get('/api/v1/auto-approve', () => HttpResponse.json(autoApproveRules)),
  http.post('/api/v1/auto-approve', () => HttpResponse.json({ ok: true })),
  http.delete('/api/v1/auto-approve/:id', () => new HttpResponse(null, { status: 204 })),

  // Tools & Plugins
  http.get('/api/v1/tools', () => HttpResponse.json(tools)),
  http.get('/api/v1/tools/:name', ({ params }) => {
    const tool = tools.find(t => t.name === params.name)
    return tool ? HttpResponse.json(tool) : new HttpResponse(null, { status: 404 })
  }),
  http.post('/api/v1/tools', () => HttpResponse.json({ ok: true })),
  http.put('/api/v1/tools/:name', () => HttpResponse.json({ ok: true })),
  http.delete('/api/v1/tools/:name', () => new HttpResponse(null, { status: 204 })),
  http.get('/api/v1/tools/:name/health', () => HttpResponse.json({ status: 'connected', uptime_secs: 3600 })),
  http.post('/api/v1/tools/:name/restart', () => HttpResponse.json({ ok: true })),
  http.get('/api/v1/tools/:name/defs', () => HttpResponse.json({ tools: [] })),
  http.get('/api/v1/plugins', () => HttpResponse.json(plugins)),
  http.get('/api/v1/plugins/:name', ({ params }) => {
    const plugin = plugins.find(p => p.name === params.name)
    return plugin ? HttpResponse.json(plugin) : new HttpResponse(null, { status: 404 })
  }),
  http.post('/api/v1/plugins', () => HttpResponse.json({ ok: true })),
  http.delete('/api/v1/plugins/:name', () => new HttpResponse(null, { status: 204 })),

  // Persona
  http.get('/api/v1/agents/:name/persona/:section', ({ params }) => {
    const content = personaSections[params.section]
    return content !== undefined
      ? HttpResponse.json({ content })
      : new HttpResponse(null, { status: 404 })
  }),
  http.put('/api/v1/agents/:name/persona/:section', () => HttpResponse.json({ ok: true })),

  // KV Store
  http.get('/api/v1/kv/:agent', () => HttpResponse.json(kvEntries)),
  http.get('/api/v1/kv/:agent/:key', ({ params }) => {
    const entry = kvEntries.find(e => e.key === params.key)
    return entry ? HttpResponse.json(entry) : new HttpResponse(null, { status: 404 })
  }),
  http.delete('/api/v1/kv/:agent/:key', () => new HttpResponse(null, { status: 204 })),

  // Browser
  http.get('/api/v1/browser/profiles', () => HttpResponse.json(browserProfiles)),
  http.get('/api/v1/browser/profiles/:name', ({ params }) => {
    const profile = browserProfiles.find(p => p.name === params.name)
    return profile ? HttpResponse.json(profile) : new HttpResponse(null, { status: 404 })
  }),
  http.delete('/api/v1/browser/profiles/:name', () => new HttpResponse(null, { status: 204 })),
  http.get('/api/v1/browser/sessions', () => HttpResponse.json(browserSessions)),
  http.get('/api/v1/browser/config', () => HttpResponse.json({ headless: true, timeout: 30000 })),

  // API Keys
  http.get('/api/v1/keys', () => HttpResponse.json(apiKeys)),
  http.post('/api/v1/keys', () => HttpResponse.json({ id: 'key-new', key: 'dk_newkey123', name: 'new-key' })),
  http.delete('/api/v1/keys/:id', () => new HttpResponse(null, { status: 204 })),
  http.post('/api/v1/keys/:id/rotate', () => HttpResponse.json({ key: 'dk_rotated123' })),
  http.delete('/api/v1/keys/:id/permanent', () => new HttpResponse(null, { status: 204 })),

  // Chat SSE
  http.post('/api/v1/chat', () => {
    const encoder = new TextEncoder()
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(encoder.encode('data: {"type":"content","text":"Hello"}\n\n'))
        controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-1"}\n\n'))
        controller.close()
      },
    })
    return new HttpResponse(stream, {
      headers: { 'Content-Type': 'text/event-stream' },
    })
  }),

  // Auth
  http.get('/auth/config', () => HttpResponse.json({ mode: 'token' })),
  http.get('/auth/session', () => HttpResponse.json({ authenticated: false })),
  http.post('/auth/login', async ({ request }) => {
    const body = await request.json()
    if (body.password === 'correct') {
      return HttpResponse.json({ token: 'session-token-123' })
    }
    return HttpResponse.json({ error: 'Invalid password' }, { status: 401 })
  }),
  http.post('/auth/logout', () => HttpResponse.json({ ok: true })),

  // Setup
  http.get('/api/v1/setup', () => HttpResponse.json({ needs_setup: false, has_account: true })),
  http.post('/api/v1/setup', () => HttpResponse.json({ key: 'dk_setup123' })),
  http.post('/api/v1/setup/account', () => HttpResponse.json({ ok: true })),

  // Server config
  http.get('/api/v1/server/config', () => HttpResponse.json({
    listen: ':8080',
    tls: false,
    rate_limit: 100,
    cors_origins: ['https://example.com'],
    websocket_enabled: true,
    websocket_max_connections: 50,
    websocket_replay_buffer_ttl: '5m',
    external_url: 'https://den.example.com',
    timezone: 'UTC',
  })),
  http.patch('/api/v1/server/config', () => HttpResponse.json({ ok: true })),
]
