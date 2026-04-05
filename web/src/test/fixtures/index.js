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
