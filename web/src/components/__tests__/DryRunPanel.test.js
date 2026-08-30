import { describe, test, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'

const DryRunPanel = (await import('../DryRunPanel.svelte')).default

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  server.use(
    // Real shape: llm.ModelInfo — top-level *_per_mtok, null = pricing unknown.
    http.get('/api/v1/models/details', () => HttpResponse.json({
      models: [
        { id: 'moonshotai/kimi-k3', name: 'Kimi K3', provider: 'openrouter', input_per_mtok: 0.8, output_per_mtok: 3.2, supports_tools: true, weekly_tokens: 0 },
        { id: 'openai/gpt-4o-mini', name: 'GPT-4o mini', provider: 'openrouter', input_per_mtok: 0.15, output_per_mtok: 0.6, supports_tools: true, weekly_tokens: 0 },
        { id: 'local/mystery', name: 'Mystery', provider: 'ollama', input_per_mtok: null, output_per_mtok: null, supports_tools: false, weekly_tokens: 0 },
      ],
    })),
  )
})

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

const okRun = () => Promise.resolve(makeTranscript())

async function clickRun() {
  await fireEvent.click(screen.getByRole('button', { name: /^Run$|^Run both$/ }))
}

describe('DryRunPanel', () => {
  test('leads with the reassurance every user needs first', () => {
    render(DryRunPanel, { props: { run: okRun } })
    expect(screen.getByText(/nothing was sent, written, or remembered/i)).toBeInTheDocument()
  })

  // A dry run spends real tokens, so opening the panel must not start one.
  test('does not run until asked', async () => {
    const run = vi.fn(okRun)
    render(DryRunPanel, { props: { run } })
    await waitFor(() => expect(screen.getByText('Run')).toBeInTheDocument())
    expect(run).not.toHaveBeenCalled()

    await clickRun()
    await waitFor(() => expect(run).toHaveBeenCalledTimes(1))
  })

  test('runs the live model when no override is chosen', async () => {
    const run = vi.fn(okRun)
    render(DryRunPanel, { props: { run, liveModel: 'moonshotai/kimi-k2.6' } })
    await clickRun()
    await waitFor(() => expect(run).toHaveBeenCalledWith(''))
  })

  test('badges the suppressed write and leaves executed calls alone', async () => {
    render(DryRunPanel, { props: { run: okRun } })
    await clickRun()

    await screen.findByText('kv_set')
    expect(screen.getByText('SUPPRESSED')).toBeInTheDocument()
    expect(screen.getByText('142 ms')).toBeInTheDocument()
  })

  test('summarises the run on one line instead of metric tiles', async () => {
    render(DryRunPanel, { props: { run: okRun } })
    await clickRun()
    expect(await screen.findByText(/3 rounds · 1 suppressed · \$0\.0184 · 6\.2 s/)).toBeInTheDocument()
  })

  test('expands a tool call to show the result the model actually saw', async () => {
    render(DryRunPanel, { props: { run: okRun } })
    await clickRun()

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
    await clickRun()

    await screen.findByText(/provider timeout/)
    await fireEvent.click(screen.getByText('Try again'))
    await screen.findByText(/Morning\. Nothing needs you/)
    expect(run).toHaveBeenCalledTimes(2)
  })

  test('handles a turn that produced no text', async () => {
    render(DryRunPanel, { props: { run: () => Promise.resolve(makeTranscript({ response: '' })) } })
    await clickRun()
    expect(await screen.findByText(/produced no text response/i)).toBeInTheDocument()
  })

  describe('compare slot', () => {
    // The empty slot is the mode switch — it must be visible before any run,
    // or the comparison feature is undiscoverable.
    test('offers the compare rail while only one model is selected', () => {
      render(DryRunPanel, { props: { run: okRun } })
      expect(screen.getByText(/Compare with/)).toBeInTheDocument()
    })

    async function pickCompare(model = 'moonshotai/kimi-k3') {
      await fireEvent.click(screen.getByText(/Compare with/))
      const option = await screen.findByRole('option', { name: new RegExp(model) })
      await fireEvent.click(option)
    }

    // Regression: priceOf() used to read a `pricing` object the endpoint never
    // returns, so the label was always empty. Known pricing must render, and
    // null (unknown) must stay blank rather than showing "$NaN".
    test('shows per-model pricing from the models/details shape', async () => {
      render(DryRunPanel, { props: { run: okRun } })
      await fireEvent.click(screen.getByText(/Compare with/))

      const priced = await screen.findByRole('option', { name: /moonshotai\/kimi-k3/ })
      expect(priced.querySelector('.menu-price')).toHaveTextContent('$0.80 / $3.20')

      const unknown = screen.getByRole('option', { name: /local\/mystery/ })
      expect(unknown.querySelector('.menu-price')).toHaveTextContent('')
      expect(unknown.textContent).not.toMatch(/\$/)
    })

    test('running with a compare model calls run twice, once per model', async () => {
      const run = vi.fn()
        .mockResolvedValueOnce(makeTranscript())
        .mockResolvedValueOnce(makeTranscript({ model: 'kimi-k3', rounds: 4, cost_usd: 0.0231 }))
      render(DryRunPanel, { props: { run } })
      await pickCompare()
      await clickRun()

      await waitFor(() => expect(run).toHaveBeenCalledTimes(2))
      expect(run).toHaveBeenNthCalledWith(1, '')
      expect(run).toHaveBeenNthCalledWith(2, 'moonshotai/kimi-k3')
    })

    test('shows deltas and marks the regression', async () => {
      const run = vi.fn()
        .mockResolvedValueOnce(makeTranscript({ rounds: 3 }))
        .mockResolvedValueOnce(makeTranscript({ model: 'kimi-k3', rounds: 4 }))
      const { container } = render(DryRunPanel, { props: { run } })
      await pickCompare()
      await clickRun()

      await waitFor(() => expect(screen.getByText('Rounds')).toBeInTheDocument())
      expect(screen.getByText('3 → 4')).toBeInTheDocument()
      expect(container.querySelector('.delta-pct.worse')).toHaveTextContent('+33%')
    })

    // Without this, a red delta between two model names reads as a verdict.
    test('says out loud that one sample is not a verdict', async () => {
      const run = vi.fn()
        .mockResolvedValueOnce(makeTranscript())
        .mockResolvedValueOnce(makeTranscript({ model: 'kimi-k3' }))
      render(DryRunPanel, { props: { run } })
      await pickCompare()
      await clickRun()

      expect(await screen.findByText(/a smoke test, not a verdict/i)).toBeInTheDocument()
    })

    test('counts bad tool args as a delta of its own', async () => {
      const clean = makeTranscript()
      const flaky = makeTranscript({
        model: 'kimi-k3',
        tool_calls: [...makeTranscript().tool_calls, {
          tool: 'web_fetch', round: 3, outcome: 'rejected', suppressed: false,
          duration_ms: 20, arguments: '{}', error: 'bad url',
        }],
      })
      const run = vi.fn().mockResolvedValueOnce(clean).mockResolvedValueOnce(flaky)
      render(DryRunPanel, { props: { run } })
      await pickCompare()
      await clickRun()

      await waitFor(() => expect(screen.getByText('Bad tool args')).toBeInTheDocument())
      expect(screen.getByText('0 → 1')).toBeInTheDocument()
    })

    test('clearing the comparison drops back to a single column', async () => {
      const run = vi.fn().mockResolvedValue(makeTranscript())
      render(DryRunPanel, { props: { run } })
      await pickCompare()
      expect(screen.getByText('vs')).toBeInTheDocument()

      await fireEvent.click(screen.getByLabelText('Remove comparison'))
      expect(screen.queryByText('vs')).not.toBeInTheDocument()
      expect(screen.getByText(/Compare with/)).toBeInTheDocument()
    })

    // A transcript rendered under a model name it did not run on would be a lie.
    test('changing the model discards the previous result', async () => {
      const run = vi.fn().mockResolvedValue(makeTranscript())
      render(DryRunPanel, { props: { run } })
      await clickRun()
      await screen.findByText(/Morning\. Nothing needs you/)

      await pickCompare()
      expect(screen.queryByText(/Morning\. Nothing needs you/)).not.toBeInTheDocument()
    })
  })
})

// A turn that exhausted its round budget answers with a wrap-up, and without
// this it reads exactly like one the model chose to finish.
describe('DryRunPanel — cut-short turns', () => {
  async function pickCompare(model = 'moonshotai/kimi-k3') {
    await fireEvent.click(screen.getByText(/Compare with/))
    const option = await screen.findByRole('option', { name: new RegExp(model) })
    await fireEvent.click(option)
  }

  test('names why the tool loop stopped, in plain language', async () => {
    const run = vi.fn().mockResolvedValue(makeTranscript({ stop_reason: 'max_rounds' }))
    render(DryRunPanel, { props: { run } })
    await clickRun()

    await waitFor(() => expect(screen.getByTestId('stop-reason')).toBeInTheDocument())
    expect(screen.getByTestId('stop-reason')).toHaveTextContent('hit the round limit')
    expect(screen.getByTestId('stop-reason')).not.toHaveTextContent('max_rounds')
  })

  test('a turn the model finished on its own carries no badge', async () => {
    render(DryRunPanel, { props: { run: okRun } })
    await clickRun()
    await screen.findByText(/Morning\. Nothing needs you/)

    expect(screen.queryByTestId('stop-reason')).not.toBeInTheDocument()
  })

  // Only one side of a comparison flailing is exactly the signal being looked
  // for, so the badge has to belong to its own column.
  test('badges only the side that was cut short', async () => {
    const run = vi.fn()
      .mockResolvedValueOnce(makeTranscript())
      .mockResolvedValueOnce(makeTranscript({ stop_reason: 'repeated_calls' }))
    render(DryRunPanel, { props: { run } })
    await pickCompare()
    await clickRun()

    await waitFor(() => expect(screen.getAllByTestId('stop-reason')).toHaveLength(1))
    expect(screen.getByTestId('stop-reason')).toHaveTextContent('repeated the same call')
  })

  // The slugs are the engine's, and it may grow one this list has not learnt.
  test('an unrecognised reason is shown rather than swallowed', async () => {
    const run = vi.fn().mockResolvedValue(makeTranscript({ stop_reason: 'budget_exhausted' }))
    render(DryRunPanel, { props: { run } })
    await clickRun()

    await waitFor(() => expect(screen.getByTestId('stop-reason'))
      .toHaveTextContent('budget_exhausted'))
  })
})

// The serving upstream is an eval-only field: a schedule or skill preview's
// payload has no such thing, and the head must not sprout an empty chip.
describe('DryRunPanel — serving upstream', () => {
  test('a preview transcript shows no upstream', async () => {
    render(DryRunPanel, { props: { run: okRun } })
    await clickRun()
    await screen.findByText(/Morning\. Nothing needs you/)

    expect(screen.queryByTestId('upstream')).not.toBeInTheDocument()
  })
})
