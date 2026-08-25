import { http, HttpResponse } from 'msw'
import {
  agents, sessions, messages, approvals, costs, skills, schedules,
  tools, plugins, browserProfiles, browserSessions, kvEntries,
  apiKeys, autoApproveRules, personaSections, auditEvents, auditStats,
  channels, sessionStats, sessionToolCalls, sessionSkillUsages,
} from './fixtures/index.js'

// --- Eval fixtures ---------------------------------------------------------
// Inline like the other server-shaped fixtures below (auth sessions, providers,
// server config) rather than in fixtures/index.js, since only the Evals page
// reads them.

export const evalTaskSets = [
  { id: 1, name: 'golden-set', description: 'Curated from live history', task_count: 37, created_at: '2026-08-01T09:00:00Z' },
  { id: 2, name: 'tool-heavy', description: '', task_count: 8, created_at: '2026-08-05T09:00:00Z' },
]

export const evalConfig = {
  default_k: 3,
  max_cost_per_run: 2,
  max_concurrent: 2,
  completeness_floor: 0.8,
  win_threshold: 0.55,
  gate_rejected_rate_pp: 2,
  gate_rounds_pct: 20,
  gate_cost_pct: 25,
  audit: 'full',
}

export const evalEstimate = {
  low: 0.12,
  high: 0.4,
  currency: 'USD',
  basis: 'history',
  tasks: 10,
  k: 1,
  per_variant: [
    { name: 'current', low: 0.06, high: 0.2, basis: 'history' },
    { name: 'openai/gpt-4o', low: 0.06, high: 0.2, basis: 'history' },
  ],
}

// One candidate per category, the stratified shape GET /eval/suggest returns.
export const evalSuggestions = {
  candidates: [
    {
      prompt: 'Summarise the on-call handover for this week',
      category: 'chat',
      conversation_id: 'chan:ops',
      message_id: 101,
      created_at: '2026-08-20T09:00:00Z',
      signals: ['high_cost'],
      preceding: [{ role: 'user', content: 'morning' }, { role: 'assistant', content: 'hi' }],
    },
    {
      prompt: '/digest yesterday',
      category: 'skill_command',
      conversation_id: 'chan:ops',
      message_id: 102,
      created_at: '2026-08-21T09:00:00Z',
      signals: ['command_skill', 'many_rounds'],
      preceding: [],
    },
    {
      prompt: 'Fetch the release notes and diff them against the changelog',
      category: 'tool_heavy',
      conversation_id: 'chan:dev',
      message_id: 103,
      created_at: '2026-08-22T09:00:00Z',
      signals: ['tool_fault', 'many_rounds'],
      preceding: [],
    },
  ],
}

export const evalRuns = [
  {
    id: 1,
    task_set_id: 1,
    base_agent: 'default',
    status: 'running',
    k: 1,
    cost_cap: 2,
    cost_spent: 0.31,
    as_of: '2026-08-18T09:00:00Z',
    created_at: '2026-08-18T09:00:00Z',
    task_count: 10,
    variants: [
      { id: 1, run_id: 1, name: 'current', overlay: '{}' },
      { id: 2, run_id: 1, name: 'openai/gpt-4o', overlay: '{"llm_model":"openai/gpt-4o"}' },
    ],
    samples_done: 8,
    samples_total: 20,
    eta_seconds: 90,
    active: true,
  },
  {
    id: 2,
    task_set_id: 1,
    base_agent: 'default',
    status: 'done',
    k: 3,
    cost_cap: 2,
    cost_spent: 1.04,
    as_of: '2026-08-17T09:00:00Z',
    created_at: '2026-08-17T09:00:00Z',
    finished_at: '2026-08-17T09:40:00Z',
    task_count: 37,
    variants: [
      { id: 3, run_id: 2, name: 'current', overlay: '{}' },
      { id: 4, run_id: 2, name: 'anthropic/claude-3-opus', overlay: '{"llm_model":"anthropic/claude-3-opus"}' },
    ],
    samples_done: 222,
    samples_total: 222,
    active: false,
  },
]

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
  http.post('/api/v1/agents', async ({ request }) => {
    const body = await request.json()
    return HttpResponse.json({
      name: body.name,
      status: 'created',
    }, { status: 201 })
  }),
  http.delete('/api/v1/agents/:name', ({ params }) => {
    if (params.name === 'default') {
      return HttpResponse.json({ error: 'cannot delete the default agent' }, { status: 400 })
    }
    return new HttpResponse(null, { status: 204 })
  }),

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
    wizard_completed: true,
  })),
  http.post('/api/v1/onboarding/dismiss', () => new HttpResponse(null, { status: 204 })),
  http.post('/api/v1/onboarding/wizard-complete', () => new HttpResponse(null, { status: 204 })),

  // Audit
  http.get('/api/v1/audit', () => HttpResponse.json({ events: auditEvents, total: auditEvents.length })),
  http.get('/api/v1/audit/stats', () => HttpResponse.json(auditStats)),

  // Evals
  http.get('/api/v1/eval/task-sets', () => HttpResponse.json(evalTaskSets)),
  http.get('/api/v1/eval/task-sets/:name', ({ params }) => {
    const set = evalTaskSets.find(s => s.name === params.name)
    return set ? HttpResponse.json({ ...set, tasks: [] }) : new HttpResponse(null, { status: 404 })
  }),
  http.post('/api/v1/eval/task-sets', async ({ request }) => {
    const body = await request.json()
    return HttpResponse.json({ id: 99, name: body.name, description: body.description || '', task_count: 0 }, { status: 201 })
  }),
  http.patch('/api/v1/eval/task-sets/:name', () => HttpResponse.json({ ok: true })),
  http.delete('/api/v1/eval/task-sets/:name', () => new HttpResponse(null, { status: 204 })),
  http.post('/api/v1/eval/task-sets/:name/tasks', () => HttpResponse.json({ id: 1 }, { status: 201 })),
  http.patch('/api/v1/eval/task-sets/:name/tasks/:id', () => HttpResponse.json({ ok: true })),
  http.delete('/api/v1/eval/task-sets/:name/tasks/:id', () => new HttpResponse(null, { status: 204 })),
  http.get('/api/v1/eval/task-sets/:name/export', () => HttpResponse.text('{"prompt":"hi","category":"chat"}\n')),
  http.post('/api/v1/eval/task-sets/:name/import', () => HttpResponse.json({ imported: 3 })),
  http.get('/api/v1/eval/config', () => HttpResponse.json(evalConfig)),
  http.post('/api/v1/eval/estimate', () => HttpResponse.json(evalEstimate)),
  http.get('/api/v1/eval/runs', () => HttpResponse.json(evalRuns)),
  http.get('/api/v1/eval/runs/:id', ({ params }) => {
    const run = evalRuns.find(r => String(r.id) === params.id)
    return run ? HttpResponse.json(run) : new HttpResponse(null, { status: 404 })
  }),
  http.post('/api/v1/eval/runs', async ({ request }) => {
    const body = await request.json()
    return HttpResponse.json({
      id: 3,
      task_set_id: 1,
      base_agent: body.base_agent,
      status: 'pending',
      k: body.k ?? 3,
      cost_cap: body.cost_cap ?? 2,
      cost_spent: 0,
      created_at: '2026-08-19T09:00:00Z',
      task_count: body.sample_tasks || 37,
      variants: (body.variants || []).map((v, i) => ({ id: 10 + i, run_id: 3, name: v.name, overlay: '{}' })),
      samples_done: 0,
      samples_total: 0,
      active: true,
    }, { status: 201 })
  }),
  http.post('/api/v1/eval/runs/:id/stop', () => HttpResponse.json({ status: 'stopping' })),
  http.get('/api/v1/eval/runs/:id/summary', () => HttpResponse.json({ variants: [], per_task: [], completeness: {}, verdicts: [] })),
  http.get('/api/v1/eval/runs/:id/samples', () => HttpResponse.json([])),
  http.get('/api/v1/eval/runs/:id/pairs', () => HttpResponse.json([])),
  http.get('/api/v1/eval/suggest', () => HttpResponse.json(evalSuggestions)),

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
      { name: 'anthropic', type: 'anthropic', enabled: false, api_key_set: false, cost_limit_soft: 5.0, cost_limit_hard: 10.0 },
      { name: 'openrouter', type: 'openrouter', enabled: true, api_key_set: true },
      { name: 'openai', type: 'openai', enabled: false, api_key_set: false },
      { name: 'ollama', type: 'ollama', enabled: true, api_key_set: false, base_url: 'http://localhost:11434' },
    ],
  })),
  http.post('/api/v1/llm/providers', () => HttpResponse.json({ name: 'new-provider', status: 'created' }, { status: 201 })),
  http.patch('/api/v1/llm/providers/:name', () => HttpResponse.json({ status: 'updated' })),
  http.delete('/api/v1/llm/providers/:name', () => new HttpResponse(null, { status: 204 })),

  // Server config
  http.get('/api/v1/server/config', () => HttpResponse.json({
    listen: ':8080',
    tls: false,
    rate_limit: 100,
    cors_origins: ['https://example.com'],
    websocket_enabled: true,
    websocket_max_connections: 50,
    websocket_replay_buffer_ttl: '5m',
    mcp_server_enabled: false,
    mcp_server_transport: 'streamable',
    mcp_server_session_timeout: '30m',
    mcp_server_chat_timeout: '2m',
    mcp_server_stateless: false,
    mcp_server_endpoint: 'http://:8080/api/v1/mcp',
    web_tools_enabled: true,
    web_fetch_max_response_chars: 8000,
    script_enabled: true,
    external_url: 'https://den.example.com',
    timezone: 'UTC',
    version: 'v0.25.0',
    commit: 'abc1234def5678',
    go_version: 'go1.22.0',
  })),
  http.patch('/api/v1/server/config', () => HttpResponse.json({ ok: true })),
  http.post('/api/v1/server/reload', () => HttpResponse.json({ ok: true })),
  http.post('/api/v1/server/restart', () => new HttpResponse(null, { status: 204 })),
]
