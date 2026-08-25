import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import { evalSampleTranscript, evalTraceCalls } from '../../api.js'

const EvalTaskDiffs = (await import('../EvalTaskDiffs.svelte')).default

// One trace as the runner writes it: []agent.ToolCallRecord, with a write
// suppressed by the eval execution policy.
const TRACE = JSON.stringify([
  {
    tool_name: 'kv_get', server_name: 'kv', round: 1, duration_ms: 120,
    success: true, outcome: 'ok', arguments: '{"key":"a"}', result: 'value',
  },
  {
    tool_name: 'telegram_send', server_name: '', round: 2, duration_ms: 0,
    success: true, outcome: 'suppressed', arguments: '{"text":"hi"}',
    result: '[suppressed]',
  },
])

const SUMMARY = {
  run_id: 7,
  status: 'done',
  baseline_variant: 'current',
  variants: [
    { variant_id: 1, name: 'current', overlay: {} },
    { variant_id: 2, name: 'openai/gpt-4o', overlay: { llm_model: 'openai/gpt-4o' } },
  ],
  per_task: [
    {
      task_id: 11,
      prompt: 'Summarise the on-call handover for this week',
      category: 'chat',
      variants: [
        {
          variant_id: 1, name: 'current', samples_ok: 1,
          mean_cost: 0.02, mean_rounds: 2, mean_latency_ms: 1500,
          delta_cost: 0, delta_rounds: 0, delta_latency_ms: 0,
        },
        {
          variant_id: 2, name: 'openai/gpt-4o', samples_ok: 1,
          mean_cost: 0.015, mean_rounds: 3, mean_latency_ms: 2500,
          delta_cost: -0.005, delta_rounds: 1, delta_latency_ms: 1000,
        },
      ],
    },
  ],
  completeness: { samples_ok: 2, samples_expected: 2, pairs: 1, pairs_judged: 1 },
  verdicts: [],
}

const SAMPLES = [
  {
    id: 101, run_id: 7, variant_id: 1, task_id: 11, k_index: 0, status: 'ok',
    response: 'Current model answer', trace: TRACE, rounds: 2,
    outcome_suppressed: 1, cost: 0.02, latency_ms: 1500,
  },
  {
    id: 102, run_id: 7, variant_id: 2, task_id: 11, k_index: 0, status: 'ok',
    response: 'Candidate model answer', trace: TRACE, rounds: 3,
    outcome_suppressed: 1, cost: 0.015, latency_ms: 2500,
  },
]

const PAIRS = {
  run_id: 7,
  baseline_variant: 'current',
  pairs: [
    {
      pair_id: 1, task_id: 11, task_prompt: 'Summarise the on-call handover for this week',
      category: 'chat', k: 0,
      baseline: { variant_id: 1, variant: 'current', sample_id: 101 },
      candidate: { variant_id: 2, variant: 'openai/gpt-4o', sample_id: 102 },
      items: [
        {
          item_id: 1, presentation_order: 'AB', status: 'judged',
          verdicts: [{
            judge_ident: 'claude-code', winner: 'B', winner_variant: 'openai/gpt-4o',
            dimensions: { correctness: 'B', tone: 'tie' },
            notes: 'Candidate followed the persona more closely.',
            rubric_version: 'v1', created_at: '2026-08-20T10:00:00Z',
          }],
        },
        {
          item_id: 2, presentation_order: 'BA', status: 'judged',
          verdicts: [{
            judge_ident: 'claude-code', winner: 'A', winner_variant: 'openai/gpt-4o',
            dimensions: { correctness: 'A' }, notes: '', rubric_version: 'v1',
            created_at: '2026-08-20T10:01:00Z',
          }],
        },
      ],
      outcome: 'win',
    },
  ],
}

let sampleCalls = 0
let pairCalls = 0

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  sampleCalls = 0
  pairCalls = 0
  server.use(
    http.get('/api/v1/eval/runs/:id/summary', () => HttpResponse.json(SUMMARY)),
    http.get('/api/v1/eval/runs/:id/samples', () => {
      sampleCalls++
      return HttpResponse.json(SAMPLES)
    }),
    http.get('/api/v1/eval/runs/:id/pairs', () => {
      pairCalls++
      return HttpResponse.json(PAIRS)
    }),
  )
})

async function renderDiffs(props = {}) {
  const r = render(EvalTaskDiffs, { runId: 7, ...props })
  await waitFor(() => expect(screen.getByTestId('task-row-11')).toBeInTheDocument())
  return r
}

async function expandFirstRow() {
  await fireEvent.click(screen.getByRole('button', { name: /Show the runs/ }))
  await waitFor(() => expect(screen.getByTestId('task-detail-11')).toBeInTheDocument())
  await waitFor(() =>
    expect(screen.queryByText(/Loading the runs/)).not.toBeInTheDocument())
}

describe('EvalTaskDiffs — rows', () => {
  test('fetches its own results when no summary is passed', async () => {
    await renderDiffs()

    expect(screen.getByText('Summarise the on-call handover for this week')).toBeInTheDocument()
    expect(screen.getByText('chat')).toBeInTheDocument()
    // Both models' numbers, in the operator's units.
    expect(screen.getByText('$0.0200')).toBeInTheDocument()
    expect(screen.getByText('$0.0150')).toBeInTheDocument()
    expect(screen.getByText('1.5 s')).toBeInTheDocument()
    expect(screen.getByText('2.5 s')).toBeInTheDocument()
  })

  test('uses a passed-in summary without refetching', async () => {
    let summaryCalls = 0
    server.use(http.get('/api/v1/eval/runs/:id/summary', () => {
      summaryCalls++
      return HttpResponse.json(SUMMARY)
    }))

    await renderDiffs({ summary: SUMMARY })

    expect(summaryCalls).toBe(0)
  })

  test('shows deltas on the candidate only, signed by direction', async () => {
    await renderDiffs()

    // Cheaper is the good direction, slower and more rounds are not.
    const cheaper = screen.getByText('−0.0050')
    expect(cheaper).toHaveClass('good')
    expect(screen.getByText('+1.0')).toHaveClass('bad')
    expect(screen.getByText('+1.0 s')).toHaveClass('bad')
    // The baseline row carries no deltas at all.
    expect(screen.queryByText('±0')).not.toBeInTheDocument()
  })

  test('names each model and its role in the comparison', async () => {
    await renderDiffs()

    expect(screen.getByText('current')).toBeInTheDocument()
    expect(screen.getByText('openai/gpt-4o')).toBeInTheDocument()
    expect(screen.getByText('Current')).toBeInTheDocument()
    expect(screen.getByText('Candidate')).toBeInTheDocument()
  })

  test('says so when the run has no per-test-case results', async () => {
    server.use(http.get('/api/v1/eval/runs/:id/summary', () =>
      HttpResponse.json({ ...SUMMARY, per_task: [] })))

    render(EvalTaskDiffs, { runId: 7 })

    await waitFor(() =>
      expect(screen.getByText(/No per-test-case results yet/)).toBeInTheDocument())
  })

  test('surfaces a failed summary fetch inline', async () => {
    server.use(http.get('/api/v1/eval/runs/:id/summary', () =>
      HttpResponse.json({ error: 'run not found' }, { status: 404 })))

    render(EvalTaskDiffs, { runId: 7 })

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('run not found'))
  })
})

describe('EvalTaskDiffs — expansion', () => {
  test('fetches nothing heavy until a row is expanded', async () => {
    await renderDiffs()

    expect(sampleCalls).toBe(0)
    expect(pairCalls).toBe(0)
  })

  test('expanding shows both runs side by side with their traces', async () => {
    await renderDiffs()
    await expandFirstRow()

    expect(sampleCalls).toBe(1)
    expect(pairCalls).toBe(1)

    expect(screen.getByText('Current model answer')).toBeInTheDocument()
    expect(screen.getByText('Candidate model answer')).toBeInTheDocument()
    // The candidate column is headed by its model, in the table and again on
    // the transcript.
    expect(screen.getAllByText('openai/gpt-4o')).toHaveLength(2)
    // Tool calls came through the adapter, suppressed one included.
    expect(screen.getAllByText('kv_get')).toHaveLength(2)
    expect(screen.getAllByText('SUPPRESSED')).toHaveLength(2)
  })

  test('shows the judge verdict, dimension scores and notes for the pair', async () => {
    await renderDiffs()
    await expandFirstRow()

    const judgment = screen.getByTestId('judgment-11-0')
    expect(judgment).toHaveTextContent('Candidate preferred')
    expect(judgment).toHaveTextContent('claude-code')
    expect(judgment).toHaveTextContent('picked openai/gpt-4o')
    expect(judgment).toHaveTextContent('correctness')
    expect(judgment).toHaveTextContent('Candidate followed the persona more closely.')
    expect(judgment).toHaveTextContent('rubric v1')
  })

  test('says when a comparison is unjudged rather than dead-ending', async () => {
    server.use(http.get('/api/v1/eval/runs/:id/pairs', () => HttpResponse.json({
      ...PAIRS,
      pairs: [{ ...PAIRS.pairs[0], items: [], outcome: 'pending' }],
    })))

    await renderDiffs()
    await expandFirstRow()

    const judgment = screen.getByTestId('judgment-11-0')
    expect(judgment).toHaveTextContent('Not judged yet')
    expect(judgment).toHaveTextContent('Nobody has judged this comparison yet.')
  })

  test('shows a failed run\'s error instead of an empty transcript', async () => {
    server.use(http.get('/api/v1/eval/runs/:id/samples', () => HttpResponse.json([
      SAMPLES[0],
      { ...SAMPLES[1], status: 'failed', error: 'provider timeout', response: '', trace: '' },
    ])))

    await renderDiffs()
    await expandFirstRow()

    expect(screen.getByText('openai/gpt-4o failed')).toBeInTheDocument()
    expect(screen.getByText('provider timeout')).toBeInTheDocument()
  })

  test('surfaces a failed detail fetch inline and retries on reopen', async () => {
    server.use(http.get('/api/v1/eval/runs/:id/samples', () =>
      HttpResponse.json({ error: 'samples unavailable' }, { status: 500 })))

    await renderDiffs()
    await fireEvent.click(screen.getByRole('button', { name: /Show the runs/ }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('samples unavailable'))
  })

  test('collapses again and does not refetch on the next open', async () => {
    await renderDiffs()
    await expandFirstRow()

    await fireEvent.click(screen.getByRole('button', { name: /Hide the runs/ }))
    await waitFor(() =>
      expect(screen.queryByTestId('task-detail-11')).not.toBeInTheDocument())

    await expandFirstRow()
    expect(sampleCalls).toBe(1)
    expect(pairCalls).toBe(1)
  })
})

describe('trace adapter', () => {
  test('maps a stored trace onto the transcript shape', () => {
    const calls = evalTraceCalls(TRACE)

    expect(calls).toHaveLength(2)
    expect(calls[0]).toEqual({
      tool: 'kv_get', server: 'kv', round: 1, outcome: 'ok', suppressed: false,
      duration_ms: 120, arguments: '{"key":"a"}', result: 'value', error: '',
    })
  })

  test('marks a suppressed write as suppressed', () => {
    const [, suppressed] = evalTraceCalls(TRACE)

    expect(suppressed.tool).toBe('telegram_send')
    expect(suppressed.outcome).toBe('suppressed')
    expect(suppressed.suppressed).toBe(true)
    expect(suppressed.duration_ms).toBe(0)
  })

  test('takes an already-decoded array as well as the stored string', () => {
    expect(evalTraceCalls(JSON.parse(TRACE))).toEqual(evalTraceCalls(TRACE))
  })

  test('yields no calls for an absent or unparseable trace', () => {
    expect(evalTraceCalls('')).toEqual([])
    expect(evalTraceCalls(undefined)).toEqual([])
    expect(evalTraceCalls('{not json')).toEqual([])
  })

  test('moves the sample-level cost and latency onto the transcript', () => {
    const t = evalSampleTranscript(SAMPLES[1], { model: 'openai/gpt-4o' })

    expect(t.model).toBe('openai/gpt-4o')
    expect(t.response).toBe('Candidate model answer')
    expect(t.rounds).toBe(3)
    expect(t.cost_usd).toBe(0.015)
    expect(t.duration_ms).toBe(2500)
    expect(t.suppressed_count).toBe(1)
    expect(t.tool_calls).toHaveLength(2)
  })

  test('falls back to counting suppressed calls when the sample has no count', () => {
    const { outcome_suppressed, ...noCount } = SAMPLES[0]

    expect(evalSampleTranscript(noCount).suppressed_count).toBe(1)
  })
})
