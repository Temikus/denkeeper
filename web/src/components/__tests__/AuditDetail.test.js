import { describe, test, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

// Mock clipboard
Object.assign(navigator, {
  clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
})

const AuditDetail = (await import('../AuditDetail.svelte')).default

function makeEvent(overrides = {}) {
  return {
    id: 'evt-1',
    category: 'tool_call',
    action: 'web_search',
    summary: 'search("test")',
    status: 'ok',
    agent: 'default',
    duration_ms: 300,
    timestamp: new Date().toISOString(),
    conversation_id: 'chan:general',
    detail: null,
    ...overrides,
  }
}

describe('AuditDetail', () => {
  test('renders nothing notable for event with no detail', () => {
    const { container } = render(AuditDetail, { props: { event: makeEvent() } })
    // Should at least show context section with agent
    expect(container.querySelector('.detail-pane')).toBeInTheDocument()
    expect(screen.getByText('default')).toBeInTheDocument()
  })

  test('shows CONTEXT section with agent and conversation_id', () => {
    render(AuditDetail, { props: { event: makeEvent() } })
    expect(screen.getByText('CONTEXT')).toBeInTheDocument()
    expect(screen.getByText('agent')).toBeInTheDocument()
    expect(screen.getByText('default')).toBeInTheDocument()
    expect(screen.getByText('session')).toBeInTheDocument()
    expect(screen.getByText('chan:general')).toBeInTheDocument()
  })

  test('shows ARGUMENTS section for tool_call with arguments', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ arguments: '{"query":"test"}' }),
      }),
    }})
    expect(screen.getByText('ARGUMENTS')).toBeInTheDocument()
  })

  test('shows compact args when arguments are short', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ arguments: '{"q":"a"}' }),
      }),
    }})
    // Short args show inline code
    const code = document.querySelector('code')
    expect(code).toBeInTheDocument()
    expect(code.textContent).toBe('{"q":"a"}')
  })

  test('shows RESULT section for tool_call with result', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ result: JSON.stringify([{ title: 'A', url: 'https://a.com' }]) }),
      }),
    }})
    expect(screen.getByText('RESULT')).toBeInTheDocument()
    expect(screen.getByText(/Array\(1\)/)).toBeInTheDocument()
  })

  test('shows field signature for array result with object items', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ result: JSON.stringify([{ title: 'R1', url: 'u1' }, { title: 'R2', url: 'u2' }]) }),
      }),
    }})
    expect(screen.getByText(/\[{title, url\}/)).toBeInTheDocument()
  })

  test('shows sample pills for array result', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ result: JSON.stringify([{ title: 'Result One' }, { title: 'Result Two' }]) }),
      }),
    }})
    expect(screen.getByText('Result One')).toBeInTheDocument()
    expect(screen.getByText('Result Two')).toBeInTheDocument()
  })

  test('shows "+ N more" pill for arrays with more than 3 items', () => {
    const items = [{ title: 'A' }, { title: 'B' }, { title: 'C' }, { title: 'D' }, { title: 'E' }]
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ result: JSON.stringify(items) }),
      }),
    }})
    expect(screen.getByText('+ 2 more')).toBeInTheDocument()
  })

  test('expands result on click', async () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ result: JSON.stringify([{ title: 'A' }]) }),
      }),
    }})
    const expandBtn = screen.getByText(/expand/)
    await fireEvent.click(expandBtn)
    expect(screen.getByText(/collapse/)).toBeInTheDocument()
    // Full JSON should be visible
    expect(document.querySelector('.result-full')).toBeInTheDocument()
  })

  test('shows ERROR section for error status events', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        status: 'error',
        detail: JSON.stringify({ error: 'Permission denied' }),
      }),
    }})
    expect(screen.getByText('ERROR')).toBeInTheDocument()
    expect(screen.getByText('Permission denied')).toBeInTheDocument()
  })

  test('shows THINKING section for LLM events with thinking_content', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({
          model: 'claude-3-opus',
          tokens: 1000, cost: 0.01,
          thinking_content: 'Let me think through this problem step by step.',
        }),
      }),
    }})
    expect(screen.getByText('THINKING')).toBeInTheDocument()
    expect(screen.getByText(/Let me think/)).toBeInTheDocument()
  })

  test('expands thinking section on click', async () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({
          model: 'claude-3-opus', tokens: 100, cost: 0.01,
          thinking_content: 'My thinking process here.',
        }),
      }),
    }})
    await fireEvent.click(screen.getByText('show'))
    expect(screen.getByText('hide')).toBeInTheDocument()
    expect(document.querySelector('.thinking-full')).toBeInTheDocument()
  })

  test('shows OUTPUT section for LLM events with response_text', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({
          model: 'claude-3-opus', tokens: 100, cost: 0.01,
          response_text: 'Here is my answer.',
        }),
      }),
    }})
    expect(screen.getByText('OUTPUT')).toBeInTheDocument()
    expect(screen.getByText('Rendered')).toBeInTheDocument()
    expect(screen.getByText('Raw')).toBeInTheDocument()
  })

  test('switches output to raw mode', async () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({
          model: 'claude-3-opus', tokens: 100, cost: 0.01,
          response_text: 'Here is my answer.',
        }),
      }),
    }})
    await fireEvent.click(screen.getByText('Raw'))
    expect(document.querySelector('.output-raw')).toBeInTheDocument()
  })

  test('shows USAGE section for LLM events', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({
          model: 'claude-3-opus',
          tokens: 1500, tokens_prompt: 1000, tokens_completion: 500,
          cost: 0.025,
          finish_reason: 'stop',
        }),
      }),
    }})
    expect(screen.getByText('USAGE')).toBeInTheDocument()
    expect(screen.getByText('$0.0250')).toBeInTheDocument()
    expect(screen.getByText('completed normally')).toBeInTheDocument()
  })

  test('shows token bar when prompt and completion tokens present', () => {
    const { container } = render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({
          model: 'claude-3-opus', tokens: 1500,
          tokens_prompt: 1000, tokens_completion: 500, cost: 0.025,
        }),
      }),
    }})
    expect(container.querySelector('.token-bar')).toBeInTheDocument()
  })

  test('shows model in CONTEXT for LLM events', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({ model: 'claude-3-opus', tokens: 100, cost: 0.01 }),
      }),
    }})
    expect(screen.getByText('model')).toBeInTheDocument()
    expect(screen.getByText('claude-3-opus')).toBeInTheDocument()
  })

  test('shows server in CONTEXT for tool_call events', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ server: 'web_search', arguments: '{}' }),
      }),
    }})
    expect(screen.getByText('server')).toBeInTheDocument()
    expect(screen.getByText(/web_search/)).toBeInTheDocument()
  })

  test('shows DETAIL section for non-tool, non-LLM events', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'approval',
        detail: JSON.stringify({ action: 'approve', tool: 'web_search' }),
      }),
    }})
    expect(screen.getByText('DETAIL')).toBeInTheDocument()
  })

  test('shows Copy as JSON action for tool_call events', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ server: 'web', arguments: '{"q":"test"}', result: '"ok"' }),
      }),
    }})
    expect(screen.getByText('Copy as JSON')).toBeInTheDocument()
  })

  test('shows Copy output action for LLM events with response_text', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({ model: 'claude-3', tokens: 100, cost: 0.01, response_text: 'Hello' }),
      }),
    }})
    expect(screen.getByText('Copy output')).toBeInTheDocument()
  })

  test('shows finish_reason "tool_calls" as "called tools"', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({ model: 'claude-3', tokens: 100, cost: 0.01, finish_reason: 'tool_calls' }),
      }),
    }})
    expect(screen.getByText('called tools')).toBeInTheDocument()
  })

  test('shows finish_reason "max_tokens" as "hit token limit"', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        category: 'llm',
        detail: JSON.stringify({ model: 'claude-3', tokens: 100, cost: 0.01, finish_reason: 'max_tokens' }),
      }),
    }})
    expect(screen.getByText('hit token limit')).toBeInTheDocument()
  })

  test('shows round number in CONTEXT when present', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ server: 'web', round: 2 }),
      }),
    }})
    expect(screen.getByText('round')).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()
  })

  test('shows truncated indicator when result_truncated is true', () => {
    render(AuditDetail, { props: {
      event: makeEvent({
        detail: JSON.stringify({ result: '"some result"', result_truncated: true }),
      }),
    }})
    expect(screen.getByText('truncated')).toBeInTheDocument()
  })
})
