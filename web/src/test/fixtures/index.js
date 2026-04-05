export const agents = [
  { name: 'default', llm_model: 'claude-3-opus', adapters: ['telegram'] },
  { name: 'helper', llm_model: 'gpt-4o', adapters: ['discord'] },
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
  { id: 'appr-1', agent: 'default', kind: 'tool_call', status: 'pending', summary: 'Run tool: web_search' },
]

export const costs = {
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
  { id: 'rule-1', agent: 'default', tool: 'web_search', scope: 'permanent' },
]

export const personaSections = {
  identity: 'You are a helpful assistant.',
  style: 'Be concise and clear.',
}
