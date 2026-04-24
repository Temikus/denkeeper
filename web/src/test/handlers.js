import { http, HttpResponse } from 'msw'
import {
  agents, sessions, messages, approvals, costs, skills, schedules,
  tools, plugins, browserProfiles, browserSessions, kvEntries,
  apiKeys, autoApproveRules, personaSections, auditEvents, auditStats,
  channels, sessionStats, sessionToolCalls, sessionSkillUsages,
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

  // Channels
  http.get('/api/v1/channels', () => HttpResponse.json(channels)),
  http.get('/api/v1/channels/:name', ({ params }) => {
    const ch = channels.find(c => c.name === params.name)
    return ch ? HttpResponse.json(ch) : new HttpResponse(null, { status: 404 })
  }),
  http.post('/api/v1/channels', async ({ request }) => {
    const body = await request.json()
    return HttpResponse.json({ name: body.name, agent: body.agent, adapters: body.adapters || [], implicit: false, session_mode: body.session_mode || '', conversation_id: `chan:${body.name}`, active_adapter_keys: [] }, { status: 201 })
  }),
  http.patch('/api/v1/channels/:name', async ({ params, request }) => {
    const body = await request.json()
    return HttpResponse.json({ name: params.name, agent: body.agent || 'default', adapters: body.adapters || [], implicit: false, session_mode: body.session_mode || '', conversation_id: `chan:${params.name}`, active_adapter_keys: [] })
  }),
  http.delete('/api/v1/channels/:name', () => new HttpResponse(null, { status: 204 })),
  http.post('/api/v1/channels/:name/activate', () => HttpResponse.json({ status: 'activated', channel: 'work', adapter_key: 'api:web-dashboard' })),
  http.delete('/api/v1/channels/:name/activate', () => HttpResponse.json({ status: 'deactivated' })),

  // Sessions
  http.get('/api/v1/sessions', () => HttpResponse.json({ sessions, total: sessions.length, limit: 50, offset: 0 })),
  http.get('/api/v1/sessions/:id/messages', () => HttpResponse.json(messages)),
  http.get('/api/v1/sessions/:id/stats', () => HttpResponse.json(sessionStats)),
  http.get('/api/v1/sessions/:id/tool-calls', () => HttpResponse.json(sessionToolCalls)),
  http.get('/api/v1/sessions/:id/skills', () => HttpResponse.json(sessionSkillUsages)),
  http.delete('/api/v1/sessions/:id', () => new HttpResponse(null, { status: 204 })),
  http.post('/api/v1/sessions/:id/clear', () => new HttpResponse(null, { status: 204 })),
  http.post('/api/v1/sessions/:id/compact', () => HttpResponse.json({ summary: 'Session compacted.' })),

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

  // Auth management
  http.get('/api/v1/auth/status', () => HttpResponse.json({
    password_enabled: true,
    oidc_enabled: false,
    sessions_trackable: true,
    active_session_count: 2,
    oidc_issuer: '',
    oidc_allowed_emails: null,
    api_keys_count: 1,
    preferred_login_method: 'auto',
  })),
  http.get('/api/v1/auth/sessions', () => HttpResponse.json({
    sessions: [
      {
        id: 'sess_abc123',
        email: 'admin@example.com',
        user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)',
        ip: '192.168.1.10',
        created_at: '2026-04-10T10:00:00Z',
        expires_at: '2026-04-17T10:00:00Z',
        last_seen_at: '2026-04-11T08:30:00Z',
      },
      {
        id: 'sess_def456',
        email: 'admin@example.com',
        user_agent: 'curl/8.4.0',
        ip: '10.0.0.1',
        created_at: '2026-04-09T14:00:00Z',
        expires_at: '2026-04-16T14:00:00Z',
        last_seen_at: '2026-04-11T06:00:00Z',
      },
    ],
    current_session_id: 'sess_abc123',
  })),
  http.delete('/api/v1/auth/sessions/:id', () => new HttpResponse(null, { status: 204 })),
  http.delete('/api/v1/auth/sessions', () => HttpResponse.json({ revoked: 2 })),
  http.post('/api/v1/auth/password', async ({ request }) => {
    const body = await request.json()
    if (body.current_password !== 'correct') {
      return HttpResponse.json({ error: 'invalid current password' }, { status: 401 })
    }
    if (!body.new_password || body.new_password.length < 8) {
      return HttpResponse.json({ error: 'new password must be at least 8 characters' }, { status: 400 })
    }
    return HttpResponse.json({ ok: true })
  }),
  http.get('/api/v1/auth/oidc/test', () => HttpResponse.json({
    ok: true,
    issuer: 'https://accounts.example.com',
    endpoints: { authorization: '/authorize', token: '/token', userinfo: '/userinfo' },
  })),
  http.post('/api/v1/auth/preferences', async ({ request }) => {
    const body = await request.json()
    if (!['auto', 'password', 'apikey'].includes(body.preferred_login_method)) {
      return HttpResponse.json({ error: 'invalid value' }, { status: 400 })
    }
    return HttpResponse.json({ ok: true })
  }),

  // Onboarding (default: all done, not shown)
  http.get('/api/v1/onboarding', () => HttpResponse.json({
    show_onboarding: false,
    steps: [
      { id: 'auth', label: 'Set up authentication', done: true },
      { id: 'agent', label: 'Configure an agent', done: true },
      { id: 'adapter', label: 'Connect a chat adapter', done: true },
      { id: 'provider', label: 'Add an LLM provider', done: true },
      { id: 'skill', label: 'Create a skill file', done: true },
    ],
    dismissed: false,
  })),
  http.post('/api/v1/onboarding/dismiss', () => new HttpResponse(null, { status: 204 })),

  // Audit
  http.get('/api/v1/audit', () => HttpResponse.json({ events: auditEvents, total: auditEvents.length })),
  http.get('/api/v1/audit/stats', () => HttpResponse.json(auditStats)),

  // Setup
  http.get('/api/v1/setup', () => HttpResponse.json({ needs_setup: false, has_account: true })),
  http.post('/api/v1/setup', () => HttpResponse.json({ key: 'dk_setup123' })),
  http.post('/api/v1/setup/account', () => HttpResponse.json({ ok: true })),

  // LLM Providers
  http.get('/api/v1/llm/providers', () => HttpResponse.json({
    default_provider: 'openrouter',
    default_model: 'anthropic/claude-3-opus',
    cost_limit_soft: 0.5,
    cost_limit_hard: 1.0,
    providers: [
      { name: 'anthropic', type: 'anthropic', enabled: false, api_key_set: false },
      { name: 'openrouter', type: 'openrouter', enabled: true, api_key_set: true },
      { name: 'openai', type: 'openai', enabled: false, api_key_set: false },
      { name: 'ollama', type: 'ollama', enabled: true, api_key_set: false, base_url: 'http://localhost:11434' },
    ],
  })),

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
    version: 'v0.25.0',
    commit: 'abc1234def5678',
    go_version: 'go1.22.0',
  })),
  http.patch('/api/v1/server/config', () => HttpResponse.json({ ok: true })),
]
