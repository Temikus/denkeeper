import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

// Mock router
vi.mock('../../router.js', async () => {
  const { writable } = await import('svelte/store')
  return {
    navigate: vi.fn(),
    currentRoute: writable('audit'),
  }
})

const AuditSession = (await import('../AuditSession.svelte')).default

const now = new Date()
const ts = (offsetMs) => new Date(now - offsetMs).toISOString()

function makeSession(overrides = {}) {
  return {
    conversation_id: 'chan:general',
    expanded: false,
    latest: ts(5000),
    timestamp: ts(60000),
    events: [
      {
        id: 'evt-1',
        category: 'tool_call',
        action: 'web_search',
        summary: 'search("test")',
        status: 'ok',
        agent: 'default',
        duration_ms: 300,
        timestamp: ts(60000),
        conversation_id: 'chan:general',
        detail: null,
      },
      {
        id: 'evt-2',
        category: 'llm',
        action: 'complete',
        summary: 'claude-3-opus',
        status: 'ok',
        agent: 'default',
        duration_ms: 1000,
        timestamp: ts(5000),
        conversation_id: 'chan:general',
        detail: JSON.stringify({ model: 'claude-3-opus', tokens: 500, cost: 0.005 }),
      },
    ],
    ...overrides,
  }
}

describe('AuditSession', () => {
  test('renders session header with SESSION label', () => {
    render(AuditSession, { props: { session: makeSession() } })
    expect(screen.getByText('SESSION')).toBeInTheDocument()
  })

  test('shows session title from first event summary', () => {
    render(AuditSession, { props: { session: makeSession() } })
    expect(screen.getByText('search("test")')).toBeInTheDocument()
  })

  test('shows session title from conversation_id when no summary', () => {
    const session = makeSession({
      events: [
        { id: 'e1', category: 'llm', action: 'complete', summary: '', status: 'ok', agent: 'default', duration_ms: 100, timestamp: ts(10000), conversation_id: 'chan:general', detail: null },
      ],
    })
    render(AuditSession, { props: { session } })
    expect(screen.getByText('general')).toBeInTheDocument()
  })

  test('shows composition chip with step counts', () => {
    render(AuditSession, { props: { session: makeSession() } })
    // Should show "1 tool · 1 llm"
    expect(screen.getByText('1 tool · 1 llm')).toBeInTheDocument()
  })

  test('shows relative time', () => {
    render(AuditSession, { props: { session: makeSession() } })
    expect(screen.getByText(/\d+s ago/)).toBeInTheDocument()
  })

  test('shows collapsed chevron by default', () => {
    const { container } = render(AuditSession, { props: { session: makeSession() } })
    const chevron = container.querySelector('.session-chevron')
    expect(chevron.textContent).toBe('▸')
  })

  test('calls onToggleSession when header is clicked', async () => {
    const onToggleSession = vi.fn()
    render(AuditSession, { props: { session: makeSession(), onToggleSession } })
    await fireEvent.click(screen.getByRole('button'))
    expect(onToggleSession).toHaveBeenCalledWith('chan:general')
  })

  test('calls onToggleSession on Enter key', async () => {
    const onToggleSession = vi.fn()
    render(AuditSession, { props: { session: makeSession(), onToggleSession } })
    await fireEvent.keyDown(screen.getByRole('button'), { key: 'Enter' })
    expect(onToggleSession).toHaveBeenCalledWith('chan:general')
  })

  test('shows expanded content when expanded is true', () => {
    const { container } = render(AuditSession, { props: { session: makeSession({ expanded: true }) } })
    expect(container.querySelector('.session-children')).toBeInTheDocument()
  })

  test('hides children when not expanded', () => {
    const { container } = render(AuditSession, { props: { session: makeSession({ expanded: false }) } })
    expect(container.querySelector('.session-children')).not.toBeInTheDocument()
  })

  test('shows USER trigger block when expanded and trigger_type is user', () => {
    const session = makeSession({
      expanded: true,
      events: [
        {
          id: 'trg-1',
          category: 'session',
          action: 'trigger',
          summary: 'User message',
          status: 'ok',
          agent: 'default',
          duration_ms: 0,
          timestamp: ts(70000),
          conversation_id: 'chan:general',
          detail: JSON.stringify({ trigger_type: 'user', prompt: 'Hello world', user_name: 'Alice', adapter: 'telegram' }),
        },
        {
          id: 'evt-1',
          category: 'llm',
          action: 'complete',
          summary: 'claude-3',
          status: 'ok',
          agent: 'default',
          duration_ms: 500,
          timestamp: ts(5000),
          conversation_id: 'chan:general',
          detail: null,
        },
      ],
    })
    render(AuditSession, { props: { session } })
    expect(screen.getByText(/USER.*Alice/)).toBeInTheDocument()
    expect(screen.getByText('Hello world')).toBeInTheDocument()
  })

  test('shows SCHEDULE trigger block when expanded and trigger_type is schedule', () => {
    const session = makeSession({
      expanded: true,
      events: [
        {
          id: 'trg-1',
          category: 'session',
          action: 'trigger',
          summary: 'Scheduled',
          status: 'ok',
          agent: 'default',
          duration_ms: 0,
          timestamp: ts(70000),
          conversation_id: 'chan:general',
          detail: JSON.stringify({ trigger_type: 'schedule', schedule_name: 'daily-check', schedule_cron: '0 9 * * *' }),
        },
        {
          id: 'evt-1',
          category: 'tool_call',
          action: 'web_search',
          summary: 'search("news")',
          status: 'ok',
          agent: 'default',
          duration_ms: 300,
          timestamp: ts(5000),
          conversation_id: 'chan:general',
          detail: null,
        },
      ],
    })
    render(AuditSession, { props: { session } })
    expect(screen.getByText('SCHEDULE')).toBeInTheDocument()
    expect(screen.getByText('daily-check')).toBeInTheDocument()
    expect(screen.getByText('0 9 * * *')).toBeInTheDocument()
  })

  test('shows error chip when some events are errors', () => {
    const session = makeSession({
      events: [
        { id: 'e1', category: 'tool_call', action: 'web_search', summary: 'search()', status: 'ok', agent: 'default', duration_ms: 100, timestamp: ts(30000), conversation_id: 'chan:general', detail: null },
        { id: 'e2', category: 'tool_call', action: 'read_file', summary: 'read_file()', status: 'error', agent: 'default', duration_ms: 50, timestamp: ts(10000), conversation_id: 'chan:general', detail: JSON.stringify({ error: 'Permission denied' }) },
      ],
    })
    render(AuditSession, { props: { session } })
    expect(screen.getByText(/recovered.*1 error/)).toBeInTheDocument()
  })

  test('shows all-error chip when all events are errors', () => {
    const session = makeSession({
      events: [
        { id: 'e1', category: 'tool_call', action: 'read_file', summary: 'read_file()', status: 'error', agent: 'default', duration_ms: 50, timestamp: ts(30000), conversation_id: 'chan:general', detail: JSON.stringify({ error: 'fail' }) },
        { id: 'e2', category: 'tool_call', action: 'read_file', summary: 'read_file()', status: 'error', agent: 'default', duration_ms: 50, timestamp: ts(10000), conversation_id: 'chan:general', detail: JSON.stringify({ error: 'fail' }) },
      ],
    })
    render(AuditSession, { props: { session } })
    expect(screen.getByText('2 errors')).toBeInTheDocument()
  })

  test('shows dot-error class when all failed', () => {
    const session = makeSession({
      events: [
        { id: 'e1', category: 'tool_call', action: 'web', summary: 'x', status: 'error', agent: 'default', duration_ms: 50, timestamp: ts(10000), conversation_id: 'chan:general', detail: JSON.stringify({ error: 'fail' }) },
      ],
    })
    const { container } = render(AuditSession, { props: { session } })
    expect(container.querySelector('.dot-error')).toBeInTheDocument()
  })

  test('title truncates long summaries', () => {
    const longSummary = 'A'.repeat(80)
    const session = makeSession({
      events: [
        { id: 'e1', category: 'llm', action: 'complete', summary: longSummary, status: 'ok', agent: 'default', duration_ms: 100, timestamp: ts(10000), conversation_id: 'chan:general', detail: null },
      ],
    })
    render(AuditSession, { props: { session } })
    // Should be truncated to 57 chars + '...'
    expect(screen.getByText(/A{57}\.\.\./)).toBeInTheDocument()
  })

  test('shows follow-up user messages inline in timeline', () => {
    const session = makeSession({
      expanded: true,
      events: [
        {
          id: 'trg-1',
          category: 'session', action: 'trigger', summary: 'First message',
          status: 'ok', agent: 'default', duration_ms: 0,
          timestamp: ts(80000), conversation_id: 'chan:general',
          detail: JSON.stringify({ trigger_type: 'user', prompt: 'First message', user_name: 'Alice', adapter: 'telegram' }),
        },
        {
          id: 'evt-1',
          category: 'tool_call', action: 'skill_get', summary: 'skill_get',
          status: 'ok', agent: 'default', duration_ms: 100,
          timestamp: ts(70000), conversation_id: 'chan:general',
          detail: null,
        },
        {
          id: 'trg-2',
          category: 'session', action: 'trigger', summary: 'Follow-up message',
          status: 'ok', agent: 'default', duration_ms: 0,
          timestamp: ts(60000), conversation_id: 'chan:general',
          detail: JSON.stringify({ trigger_type: 'user', prompt: 'Yes please fix it', user_name: 'Alice', adapter: 'telegram' }),
        },
        {
          id: 'evt-2',
          category: 'tool_call', action: 'skill_update', summary: 'skill_update',
          status: 'ok', agent: 'default', duration_ms: 200,
          timestamp: ts(50000), conversation_id: 'chan:general',
          detail: null,
        },
      ],
    })
    render(AuditSession, { props: { session } })
    // First trigger shown as header block
    expect(screen.getByText('First message')).toBeInTheDocument()
    // Follow-up trigger shown inline
    expect(screen.getByText('Yes please fix it')).toBeInTheDocument()
  })

  test('follow-up trigger handles malformed detail gracefully', () => {
    const session = makeSession({
      expanded: true,
      events: [
        {
          id: 'trg-1',
          category: 'session', action: 'trigger', summary: 'First',
          status: 'ok', agent: 'default', duration_ms: 0,
          timestamp: ts(80000), conversation_id: 'chan:general',
          detail: JSON.stringify({ trigger_type: 'user', prompt: 'First', user_name: 'Bob' }),
        },
        {
          id: 'trg-2',
          category: 'session', action: 'trigger', summary: 'Second',
          status: 'ok', agent: 'default', duration_ms: 0,
          timestamp: ts(60000), conversation_id: 'chan:general',
          detail: 'invalid json {{{',
        },
      ],
    })
    // Should not throw — null detail falls back to empty object
    const { container } = render(AuditSession, { props: { session } })
    const inlineTriggers = container.querySelectorAll('.inline-trigger')
    expect(inlineTriggers.length).toBe(1)
    // Avatar should show '?' for missing user_name
    expect(inlineTriggers[0].querySelector('.trigger-avatar').textContent).toBe('?')
  })

  test('follow-up trigger is clickable to expand', async () => {
    const onToggleRow = vi.fn()
    const session = makeSession({
      expanded: true,
      events: [
        {
          id: 'trg-1',
          category: 'session', action: 'trigger', summary: 'First',
          status: 'ok', agent: 'default', duration_ms: 0,
          timestamp: ts(80000), conversation_id: 'chan:general',
          detail: JSON.stringify({ trigger_type: 'user', prompt: 'First', user_name: 'Alice' }),
        },
        {
          id: 'trg-2',
          category: 'session', action: 'trigger', summary: 'Second',
          status: 'ok', agent: 'default', duration_ms: 0,
          timestamp: ts(60000), conversation_id: 'chan:general',
          detail: JSON.stringify({ trigger_type: 'user', prompt: 'A follow-up question', user_name: 'Alice' }),
        },
      ],
    })
    render(AuditSession, { props: { session, onToggleRow } })
    const inlineTrigger = screen.getByText('A follow-up question').closest('[role="button"]')
    await fireEvent.click(inlineTrigger)
    expect(onToggleRow).toHaveBeenCalledWith('trg-2')
  })
})
