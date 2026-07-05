export const agents = [
  { name: 'default', llm_model: 'claude-3-opus', model: 'claude-3-opus', adapters: ['telegram'], permission_tier: 'autonomous', skill_count: 2, has_tools: true, fallbacks: [] },
  { name: 'helper', llm_model: 'gpt-4o', model: 'gpt-4o', adapters: ['discord'], permission_tier: 'supervised', skill_count: 0, has_tools: false, fallbacks: [] },
]

export const sessions = [
  { id: 'sess-1', agent: 'default', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T12:00:00Z', message_count: 12, channel: 'work', total_cost: 0.0284, last_model: 'claude-3-opus', last_provider: 'anthropic', total_tokens_prompt: 4200, total_tokens_completion: 1800 },
  { id: 'sess-2', agent: 'helper', created_at: '2026-01-02T00:00:00Z', message_count: 5, channel: '', total_cost: 0, last_model: '', last_provider: '', total_tokens_prompt: 0, total_tokens_completion: 0 },
]

export const sessionStats = {
  conversation_id: 'sess-1',
  total_messages: 12,
  total_cost: 0.0284,
  total_tokens_prompt: 4200,
  total_tokens_completion: 1800,
  total_tokens_cached: 500,
  total_tool_calls: 3,
  total_tool_errors: 0,
  last_model: 'claude-3-opus',
  last_provider: 'anthropic',
  updated_at: '2026-01-01T12:00:00Z',
}

export const sessionToolCalls = [
  { id: 1, tool_name: 'web_search', server_name: 'search-mcp', round: 1, duration_ms: 1200, success: true, error_msg: '', created_at: '2026-01-01T10:00:00Z' },
  { id: 2, tool_name: 'read_file', server_name: 'fs-mcp', round: 2, duration_ms: 45, success: true, error_msg: '', created_at: '2026-01-01T10:01:00Z' },
]

export const sessionSkillUsages = [
  { id: 1, skill_name: 'greeting', match_type: 'trigger', created_at: '2026-01-01T10:00:00Z' },
]

export const messages = [
  { role: 'user', content: 'Hello' },
  { role: 'assistant', content: 'Hi there' },
]

export const approvals = [
  { id: 'appr-1', agent_name: 'default', kind: 'tool_call', status: 'pending', summary: 'Run tool: web_search', created_at: '2026-01-01T10:00:00Z', expires_at: '2026-01-01T11:00:00Z' },
]

export const costs = {
  global_cost: 1.25,
  max_per_session: 5.0,
  session_count: 2,
  session_costs: { 'sess-1': 0.75, 'sess-2': 0.50 },
  agents: {
    default: { total_cost_usd: 1.25, total_input_tokens: 50000, total_output_tokens: 10000 },
  },
}

export const skills = [
  { name: 'greeting', agent: 'default', triggers: ['hello', 'hi'], content: 'Greet the user warmly.' },
]

export const schedules = [
  {
    name: 'daily-check', agent: 'default', expression: '0 9 * * *', skill: 'report',
    channel: 'telegram:123', session_mode: 'isolated', enabled: true,
    last_run: '2026-01-01T09:00:00Z', next_run: '2026-01-02T09:00:00Z',
  },
  {
    name: 'helper-sync', agent: 'helper', expression: '@hourly', channel: 'discord:99',
    session_tier: 'restricted', enabled: false,
  },
]

export const tools = [
  { name: 'web_search', type: 'stdio', command: 'search', status: 'connected' },
]

export const plugins = [
  { name: 'example-plugin', type: 'subprocess', status: 'running' },
]

export const browserProfiles = [
  { name: 'default', user_agent: 'Mozilla/5.0', headless: true },
]

export const browserSessions = [
  { id: 'bsess-1', profile: 'default', url: 'https://example.com' },
]

export const kvEntries = [
  { key: 'user:pref', value: '{"theme":"dark"}', ttl: 3600 },
]

export const apiKeys = [
  { id: 'key-1', name: 'test-key', scopes: ['chat', 'agents:read'], created_at: '2026-01-01T00:00:00Z' },
]

export const autoApproveRules = [
  { id: 'rule-1', agent_name: 'default', tool_name: 'web_search', scope: 'permanent', created_at: '2026-01-01T00:00:00Z', created_by: 'api' },
]

export const personaSections = {
  identity: '---\nname: TestBot\nemoji: "🤖"\ntheme: helpful and concise\n---\n\nAdditional notes.',
  style: 'Be concise and clear.',
}

export const channels = [
  {
    name: 'work',
    agent: 'default',
    adapters: ['telegram'],
    implicit: false,
    session_mode: 'persistent',
    conversation_id: 'chan:work',
    active_adapter_keys: ['telegram:387956986'],
  },
  {
    name: 'personal',
    agent: 'helper',
    adapters: ['discord:123456'],
    implicit: false,
    session_mode: 'persistent',
    conversation_id: 'chan:personal',
    active_adapter_keys: [],
  },
  {
    name: 'scratch',
    agent: 'default',
    adapters: ['telegram'],
    implicit: false,
    session_mode: 'ephemeral',
    conversation_id: 'chan:scratch',
    active_adapter_keys: [],
  },
  {
    name: 'default-telegram',
    agent: 'default',
    adapters: ['telegram'],
    implicit: true,
    session_mode: 'persistent',
    conversation_id: 'chan:default-telegram',
    active_adapter_keys: [],
  },
]

export const auditEvents = [
  {
    id: 'evt-1',
    category: 'tool_call',
    action: 'web_search',
    summary: 'search("denkeeper")',
    status: 'ok',
    agent: 'default',
    source: 'telegram',
    duration_ms: 320,
    timestamp: new Date(Date.now() - 30000).toISOString(),
    conversation_id: 'chan:general',
    detail: JSON.stringify({ server: 'web_search', arguments: '{"query":"denkeeper"}', result: JSON.stringify([{ title: 'Result 1', url: 'https://example.com' }]) }),
  },
  {
    id: 'evt-2',
    category: 'llm',
    action: 'complete',
    summary: 'claude-3-opus',
    status: 'ok',
    agent: 'default',
    source: 'telegram',
    duration_ms: 1500,
    timestamp: new Date(Date.now() - 25000).toISOString(),
    conversation_id: 'chan:general',
    detail: JSON.stringify({ model: 'claude-3-opus', tokens: 1234, cost: 0.025, response_text: 'Hello, world!', thinking_content: 'Let me think...' }),
  },
  {
    id: 'evt-3',
    category: 'approval',
    action: 'approve',
    summary: 'web_search approved',
    status: 'ok',
    agent: 'default',
    source: 'api',
    duration_ms: 0,
    timestamp: new Date(Date.now() - 20000).toISOString(),
    conversation_id: null,
    detail: JSON.stringify({}),
  },
  {
    id: 'evt-4',
    category: 'tool_call',
    action: 'read_file',
    summary: 'read_file("/etc/passwd")',
    status: 'error',
    agent: 'default',
    source: 'telegram',
    duration_ms: 50,
    timestamp: new Date(Date.now() - 15000).toISOString(),
    conversation_id: 'chan:general',
    detail: JSON.stringify({ server: 'filesystem', error: 'Permission denied' }),
  },
  {
    id: 'evt-5',
    category: 'session',
    action: 'trigger',
    summary: 'User message received',
    status: 'ok',
    agent: 'default',
    source: 'telegram',
    duration_ms: 0,
    timestamp: new Date(Date.now() - 35000).toISOString(),
    conversation_id: 'chan:general',
    detail: JSON.stringify({ trigger_type: 'user', prompt: 'Search for denkeeper', user_name: 'Alice', adapter: 'telegram' }),
  },
]

export const auditStats = {
  total: 5,
  by_category: { tool_call: 2, llm: 1, approval: 1, session: 1 },
  by_status: { ok: 4, error: 1 },
}
