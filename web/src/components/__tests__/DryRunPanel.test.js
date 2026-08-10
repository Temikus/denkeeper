import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'

const DryRunPanel = (await import('../DryRunPanel.svelte')).default

function makeTranscript(overrides = {}) {
  return {
    agent: 'pamela',
    conversation_id: 'dryrun:abc123',
    as_of: '2026-07-06T07:00:00Z',
    prompt: '[Scheduled: heartbeat | ...]',
    response: 'Morning. Nothing needs you before the standup.',
    rounds: 3,
    duration_ms: 6200,
    model: 'kimi-k2.6',
    provider: 'openrouter',
    tokens_prompt: 1200,
    tokens_total: 1580,
    cost_usd: 0.0184,
    suppressed_count: 1,
    tool_calls: [
      {
        tool: 'kv_list', server: 'config', round: 1, outcome: 'ok', suppressed: false,
        duration_ms: 142, arguments: '{"prefix":"log:heartbeat"}', result: 'log:heartbeat:2026-07-05',
      },
      {
        tool: 'kv_set', server: 'config', round: 2, outcome: 'suppressed', suppressed: true,
        duration_ms: 0, arguments: '{"key":"log:heartbeat:2026-07-06"}',
        result: '[dry-run: write suppressed — kv_set not executed; assume success]',
      },
    ],
    ...overrides,
  }
}

describe('DryRunPanel', () => {
  test('leads with the reassurance every user needs first', async () => {
    render(DryRunPanel, { props: { run: () => Promise.resolve(makeTranscript()) } })
    expect(screen.getByText(/nothing was sent, written, or remembered/i)).toBeInTheDocument()
  })

  test('shows a spinner state while the turn is running', async () => {
    let resolve
    const pending = new Promise((r) => { resolve = r })
    render(DryRunPanel, { props: { run: () => pending } })

    expect(screen.getByText(/Running through the agent/i)).toBeInTheDocument()
    resolve(makeTranscript())
    await waitFor(() => expect(screen.queryByText(/Running through the agent/i)).not.toBeInTheDocument())
  })

  test('badges the suppressed write and leaves executed calls alone', async () => {
    render(DryRunPanel, { props: { run: () => Promise.resolve(makeTranscript()) } })

    await screen.findByText('kv_set')
    expect(screen.getByText('SUPPRESSED')).toBeInTheDocument()
    // The executed call shows its real duration, not a badge.
    expect(screen.getByText('142 ms')).toBeInTheDocument()
  })

  test('summarises rounds, suppressed count and cost', async () => {
    const { container } = render(DryRunPanel, { props: { run: () => Promise.resolve(makeTranscript()) } })

    await screen.findByText('$0.0184')
    expect(screen.getByText('Suppressed')).toBeInTheDocument()
    expect(screen.getByText('6.2 s')).toBeInTheDocument()
    // The suppressed count is the one accented number in the meta bar.
    expect(container.querySelector('.metric-value.accented')).toHaveTextContent('1')
  })

  test('marks the response as undelivered', async () => {
    render(DryRunPanel, { props: { run: () => Promise.resolve(makeTranscript()) } })

    await screen.findByText(/Morning\. Nothing needs you/)
    expect(screen.getByText(/Response — not delivered/i)).toBeInTheDocument()
  })

  test('expands a tool call to show the result the model actually saw', async () => {
    render(DryRunPanel, { props: { run: () => Promise.resolve(makeTranscript()) } })

    const row = await screen.findByText('kv_set')
    expect(screen.queryByText(/write suppressed — kv_set not executed/)).not.toBeInTheDocument()

    await fireEvent.click(row.closest('button'))
    expect(screen.getByText(/write suppressed — kv_set not executed/)).toBeInTheDocument()
  })

  test('surfaces an error inline with a retry', async () => {
    const run = vi.fn()
      .mockRejectedValueOnce(new Error('dry run failed: provider timeout'))
      .mockResolvedValueOnce(makeTranscript())
    render(DryRunPanel, { props: { run } })

    await screen.findByText(/provider timeout/)
    await fireEvent.click(screen.getByText('Try again'))
    await screen.findByText(/Morning\. Nothing needs you/)
    expect(run).toHaveBeenCalledTimes(2)
  })

  test('handles a turn that produced no text', async () => {
    render(DryRunPanel, { props: { run: () => Promise.resolve(makeTranscript({ response: '' })) } })
    expect(await screen.findByText(/produced no text response/i)).toBeInTheDocument()
  })

  test('omits the trace section when no tools ran', async () => {
    render(DryRunPanel, {
      props: { run: () => Promise.resolve(makeTranscript({ tool_calls: [], suppressed_count: 0 })) },
    })
    await screen.findByText(/Morning\. Nothing needs you/)
    expect(screen.queryByText('Tool trace')).not.toBeInTheDocument()
  })
})
