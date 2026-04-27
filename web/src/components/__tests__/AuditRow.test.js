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

const AuditRow = (await import('../AuditRow.svelte')).default

function makeEvent(overrides = {}) {
  return {
    id: 'evt-1',
    category: 'tool_call',
    action: 'web_search',
    summary: 'search("test")',
    status: 'ok',
    agent: 'default',
    duration_ms: 320,
    timestamp: new Date(Date.now() - 30000).toISOString(),
    conversation_id: 'chan:general',
    detail: null,
    ...overrides,
  }
}

describe('AuditRow', () => {
  test('renders tool_call event with TOOL badge', () => {
    render(AuditRow, { props: { event: makeEvent() } })
    expect(screen.getByText('TOOL')).toBeInTheDocument()
    expect(screen.getByText('search("test")')).toBeInTheDocument()
  })

  test('renders llm event with LLM badge', () => {
    render(AuditRow, { props: {
      event: makeEvent({
        id: 'evt-2', category: 'llm', action: 'complete', summary: 'claude-3-opus',
        detail: JSON.stringify({ model: 'claude-3-opus', tokens: 1000, cost: 0.01 }),
      }),
    }})
    expect(screen.getByText('LLM')).toBeInTheDocument()
    expect(screen.getByText('claude-3-opus')).toBeInTheDocument()
  })

  test('renders approval event with APPROVE badge', () => {
    render(AuditRow, { props: {
      event: makeEvent({ category: 'approval', action: 'approve', summary: 'web_search approved' }),
    }})
    expect(screen.getByText('APPROVE')).toBeInTheDocument()
    expect(screen.getByText('approve')).toBeInTheDocument()
  })

  test('renders schedule event with SCHED badge', () => {
    render(AuditRow, { props: {
      event: makeEvent({ category: 'schedule', action: 'fire', summary: 'daily-check fired' }),
    }})
    expect(screen.getByText('SCHED')).toBeInTheDocument()
  })

  test('renders session event with SESSION badge', () => {
    render(AuditRow, { props: {
      event: makeEvent({ category: 'session', action: 'trigger', summary: 'User message' }),
    }})
    expect(screen.getByText('SESSION')).toBeInTheDocument()
  })

  test('renders channel event with CHAN badge', () => {
    render(AuditRow, { props: {
      event: makeEvent({ category: 'channel', action: 'switch', summary: 'Switched to general' }),
    }})
    expect(screen.getByText('CHAN')).toBeInTheDocument()
  })

  test('shows FAILED pill for error status', () => {
    render(AuditRow, { props: {
      event: makeEvent({ status: 'error', detail: JSON.stringify({ error: 'Connection failed' }) }),
    }})
    expect(screen.getByText('FAILED')).toBeInTheDocument()
  })

  test('shows inline error message when collapsed and errored', () => {
    render(AuditRow, { props: {
      event: makeEvent({ status: 'error', detail: JSON.stringify({ error: 'Permission denied' }) }),
      expanded: false,
    }})
    expect(screen.getByText('Permission denied')).toBeInTheDocument()
  })

  test('shows tool result chip for ok tool_call with JSON array result', () => {
    render(AuditRow, { props: {
      event: makeEvent({
        detail: JSON.stringify({ server: 'web', result: JSON.stringify([{ title: 'A' }, { title: 'B' }]) }),
      }),
    }})
    expect(screen.getByText('2 items')).toBeInTheDocument()
  })

  test('shows tool result chip for ok tool_call with JSON object result', () => {
    render(AuditRow, { props: {
      event: makeEvent({
        detail: JSON.stringify({ server: 'web', result: JSON.stringify({ status: 'ok', count: 5 }) }),
      }),
    }})
    expect(screen.getByText('object · 2 keys')).toBeInTheDocument()
  })

  test('shows size chip for non-JSON string result', () => {
    render(AuditRow, { props: {
      event: makeEvent({
        detail: JSON.stringify({ server: 'web', result: 'plain text result' }),
      }),
    }})
    // Should show byte size
    expect(screen.getByText('17 B')).toBeInTheDocument()
  })

  test('shows thinking badge for llm events with thinking content', () => {
    render(AuditRow, { props: {
      event: makeEvent({
        category: 'llm', action: 'complete', summary: 'claude-3',
        detail: JSON.stringify({ model: 'claude-3', tokens: 100, cost: 0.01, thinking_content: 'reasoning' }),
      }),
    }})
    expect(screen.getByText('+ thinking')).toBeInTheDocument()
  })

  test('shows duration in row meta for tool_call with server', () => {
    render(AuditRow, { props: {
      event: makeEvent({
        detail: JSON.stringify({ server: 'web_search' }),
        duration_ms: 500,
      }),
    }})
    expect(screen.getByText(/web_search.*500ms/)).toBeInTheDocument()
  })

  test('shows duration for events without server', () => {
    render(AuditRow, { props: {
      event: makeEvent({ detail: null, duration_ms: 1500 }),
    }})
    expect(screen.getByText('1.5s')).toBeInTheDocument()
  })

  test('calls ontoggle when clicked', async () => {
    const ontoggle = vi.fn()
    render(AuditRow, { props: { event: makeEvent(), ontoggle } })
    await fireEvent.click(screen.getByRole('button'))
    expect(ontoggle).toHaveBeenCalled()
  })

  test('calls ontoggle on Enter key', async () => {
    const ontoggle = vi.fn()
    render(AuditRow, { props: { event: makeEvent(), ontoggle } })
    await fireEvent.keyDown(screen.getByRole('button'), { key: 'Enter' })
    expect(ontoggle).toHaveBeenCalled()
  })

  test('shows chevron collapsed state', () => {
    const { container } = render(AuditRow, { props: { event: makeEvent(), expanded: false } })
    const chevron = container.querySelector('.chevron')
    expect(chevron.textContent).toBe('▸')
  })

  test('shows chevron expanded state', () => {
    const { container } = render(AuditRow, { props: { event: makeEvent(), expanded: true } })
    const chevron = container.querySelector('.chevron')
    expect(chevron.textContent).toBe('▾')
  })

  test('shows detail panel when expanded', () => {
    const { container } = render(AuditRow, { props: {
      event: makeEvent({ detail: JSON.stringify({ server: 'web' }) }),
      expanded: true,
    }})
    const panel = container.querySelector('.detail-panel')
    expect(panel.classList.contains('open')).toBe(true)
  })

  test('shows status dot in standalone mode', () => {
    const { container } = render(AuditRow, { props: { event: makeEvent(), standalone: true } })
    expect(container.querySelector('.status-dot')).toBeInTheDocument()
  })

  test('shows relative time in standalone mode', () => {
    render(AuditRow, { props: { event: makeEvent(), standalone: true } })
    // timestamp is 30s ago
    expect(screen.getByText(/\d+s ago/)).toBeInTheDocument()
  })

  test('shows session link for llm events with conversation_id', () => {
    render(AuditRow, { props: {
      event: makeEvent({
        category: 'llm', action: 'complete', summary: 'claude-3',
        detail: JSON.stringify({ model: 'claude-3', tokens: 100, cost: 0.01 }),
        conversation_id: 'chan:general',
      }),
    }})
    const link = screen.getByRole('link')
    expect(link).toBeInTheDocument()
  })

  test('compact mode does not show error-bg class', () => {
    const { container } = render(AuditRow, { props: {
      event: makeEvent({ status: 'error', detail: JSON.stringify({ error: 'fail' }) }),
      compact: true,
    }})
    expect(container.querySelector('.error-bg')).not.toBeInTheDocument()
  })

  test('null result shows nothing for result chip', () => {
    render(AuditRow, { props: {
      event: makeEvent({
        detail: JSON.stringify({ server: 'web', result: JSON.stringify(null) }),
      }),
    }})
    expect(screen.getByText('null')).toBeInTheDocument()
  })

  test('renders mcp event with MCP badge', () => {
    render(AuditRow, { props: {
      event: makeEvent({ category: 'mcp', action: 'restart', summary: 'server restarted' }),
    }})
    expect(screen.getByText('MCP')).toBeInTheDocument()
  })

  test('renders supervisor event with SUPER badge', () => {
    const { container } = render(AuditRow, { props: {
      event: makeEvent({
        category: 'supervisor', action: 'review',
        summary: 'APPROVE web_search: query is benign',
        detail: JSON.stringify({ tool: 'web_search', decision: 'APPROVE', reason: 'query is benign' }),
      }),
    }})
    expect(screen.getByText('SUPER')).toBeInTheDocument()
    expect(container.querySelector('.cat-supervisor')).toBeInTheDocument()
  })

  test('shows APPROVE decision pill on supervisor approve event', () => {
    const { container } = render(AuditRow, { props: {
      event: makeEvent({
        category: 'supervisor', status: 'ok',
        summary: 'APPROVE web_search',
        detail: JSON.stringify({ tool: 'web_search', decision: 'APPROVE', reason: 'safe' }),
      }),
    }})
    expect(screen.getByText('APPROVE')).toBeInTheDocument()
    expect(container.querySelector('.pill-decision-approve')).toBeInTheDocument()
  })

  test('shows DENY decision pill on supervisor deny event', () => {
    const { container } = render(AuditRow, { props: {
      event: makeEvent({
        category: 'supervisor', status: 'denied',
        summary: 'DENY dangerous_tool',
        detail: JSON.stringify({ tool: 'dangerous_tool', decision: 'DENY', reason: 'unsafe' }),
      }),
    }})
    expect(screen.getByText('DENY')).toBeInTheDocument()
    expect(container.querySelector('.pill-decision-deny')).toBeInTheDocument()
  })

  test('shows ESCALATE decision pill on supervisor escalate event', () => {
    const { container } = render(AuditRow, { props: {
      event: makeEvent({
        category: 'supervisor', status: 'pending',
        summary: 'ESCALATE odd_tool',
        detail: JSON.stringify({ tool: 'odd_tool', decision: 'ESCALATE', reason: 'unclear' }),
      }),
    }})
    expect(screen.getByText('ESCALATE')).toBeInTheDocument()
    expect(container.querySelector('.pill-decision-escalate')).toBeInTheDocument()
  })

  test('does not show decision pill for non-supervisor events', () => {
    const { container } = render(AuditRow, { props: {
      event: makeEvent({
        category: 'llm', action: 'complete', summary: 'response',
        detail: JSON.stringify({ model: 'claude', decision: 'APPROVE' }),
      }),
    }})
    expect(container.querySelector('.pill-decision')).not.toBeInTheDocument()
  })
})
