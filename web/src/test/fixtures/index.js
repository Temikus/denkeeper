export const agents = [
  { name: 'default', llm_model: 'claude-3-opus', model: 'claude-3-opus', adapters: ['telegram'], permission_tier: 'autonomous', skill_count: 2, has_tools: true, fallbacks: [] },
  { name: 'helper', llm_model: 'gpt-4o', model: 'gpt-4o', adapters: ['discord'], permission_tier: 'supervised', skill_count: 0, has_tools: false, fallbacks: [] },
]

export const sessions = [
  { id: 'sess-1', agent: 'default', created_at: '2026-01-01T00:00:00Z', message_count: 2 },
  { id: 'sess-2', agent: 'helper', created_at: '2026-01-02T00:00:00Z', message_count: 5 },
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
  { name: 'daily-check', agent: 'default', cron: '0 9 * * *', message: 'Good morning', enabled: true },
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
