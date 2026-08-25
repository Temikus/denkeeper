import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { evalSummary, evalSamples, evalPairs } from '../../test/handlers.js'
import { token, authMode } from '../../store.js'
import { evalSampleTranscript } from '../../api.js'
import EvalResults from '../EvalResults.svelte'

const RUN = {
  id: 2,
  base_agent: 'default',
  task_set_id: 1,
  status: 'done',
  k: 1,
  task_count: 37,
}

const AGENT = { name: 'default', model: 'kimi-k2.6', provider: 'openrouter' }

/** Swaps in a summary derived from the shipped fixture. */
function withSummary(patch) {
  server.use(
    http.get('/api/v1/eval/runs/:id/summary', () =>
      HttpResponse.json({ ...evalSummary, ...patch })),
  )
}

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('EvalResults — verdict banner', () => {
  test('shows the verdict with its reason, gates, tally and categories', async () => {
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('verdict-4')).toBeInTheDocument())
    expect(screen.getByTestId('verdict-label-4')).toHaveTextContent('Upgrade')
    expect(screen.getByTestId('verdict-reason-4')).toHaveTextContent(/no gate regressed/)

    // Gate table: every gate is a row with its own pass mark.
    const gates = screen.getByTestId('gates-4')
    expect(gates).toHaveTextContent('Rejected tool calls')
    expect(gates).toHaveTextContent('Rounds per test case')
    expect(gates).toHaveTextContent('Cost per test case')
    expect(screen.getByTestId('gate-mean_cost_per_task-4')).toHaveTextContent('pass')
    // Relative gates read in %, rate gates in percentage points.
    expect(gates).toHaveTextContent('+8.3%')
    expect(gates).toHaveTextContent('-1.0 pp')

    expect(screen.getByTestId('tally-4')).toHaveTextContent(
      'Candidate won 23, lost 9, tied 5 of 37 judged comparisons')
    expect(screen.getByTestId('tally-4')).toHaveTextContent('62.0%')
    expect(screen.getByTestId('agreement-4')).toHaveTextContent('4 of 5 spot checks')
    expect(screen.getByTestId('rubric-4')).toHaveTextContent('Rubric v1')

    // Categories are labelled, not shown as their stored slugs.
    const cats = screen.getByTestId('categories-4')
    expect(cats).toHaveTextContent('Tool-heavy')
    expect(cats).toHaveTextContent('Chat / persona')
    expect(cats).not.toHaveTextContent('tool_heavy')
  })

  test('flags a failed gate and a regressed category, and appends divergence', async () => {
    withSummary({
      verdicts: [{
        ...evalSummary.verdicts[0],
        verdict: 'downgrade',
        reason: 'downgrade: mean rounds regressed +35% against a +20% threshold',
        gates: [
          ...evalSummary.verdicts[0].gates.slice(0, 1),
          { name: 'mean_rounds', baseline: 2.4, value: 3.2, delta: 35, threshold: 20, unit: '%', pass: false },
        ],
        categories: [{ ...evalSummary.verdicts[0].categories[0], regressed: true }],
        divergence: 'wins overall; regresses on tool_heavy',
      }],
    })
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('verdict-label-4')).toHaveTextContent('Downgrade'))
    expect(screen.getByTestId('gate-mean_rounds-4')).toHaveTextContent('fail')
    expect(screen.getByTestId('regressed-tool_heavy')).toBeInTheDocument()
    expect(screen.getByTestId('divergence-4')).toHaveTextContent('regresses on tool_heavy')
    // A downgrade never offers to apply itself.
    expect(screen.queryByTestId('apply-4')).not.toBeInTheDocument()
  })

  test('a run with nothing judged says the gates stand alone', async () => {
    withSummary({
      verdicts: [{
        ...evalSummary.verdicts[0],
        verdict: 'no_regressions',
        reason: 'no regressions detected; nothing judged yet',
        judgment: { pairs: 0, judged_pairs: 0, wins: 0, losses: 0, ties: 0, win_rate: 0, win_threshold: 0.55 },
      }],
    })
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('verdict-label-4'))
      .toHaveTextContent('No regressions detected'))
    expect(screen.getByTestId('no-judgment-4')).toBeInTheDocument()
    expect(screen.queryByTestId('judgment-pending-4')).not.toBeInTheDocument()
  })
})

describe('EvalResults — judgment pending', () => {
  test('names the outstanding work and hands over a copyable command', async () => {
    withSummary({
      verdicts: [{
        ...evalSummary.verdicts[0],
        verdict: 'no_regressions',
        judgment: { ...evalSummary.verdicts[0].judgment, pairs: 37, judged_pairs: 12 },
      }],
    })
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('judgment-pending-4')).toBeInTheDocument())
    expect(screen.getByTestId('judgment-pending-4')).toHaveTextContent('12 of 37 comparisons are judged')
    expect(screen.getByTestId('judge-command')).toHaveTextContent(
      'claude -p "judge pending pairs for eval run 2"')
    // The least-privilege note travels with the command.
    expect(screen.getByTestId('judgment-pending-4')).toHaveTextContent('eval:read')
  })

  test('a clean quick check offers the full eval with the same candidate', async () => {
    let picked = null
    render(EvalResults, {
      props: { run: RUN, agent: AGENT, quick: true, onrunfull: (p) => { picked = p } },
    })

    await waitFor(() => expect(screen.getByTestId('escalate-4')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('escalate-4'))
    expect(picked).toEqual({
      model: 'anthropic/claude-3-opus', provider: 'openrouter', taskSet: 'golden-set',
    })
  })

  test('a quick check with a failed gate does not offer to escalate', async () => {
    withSummary({
      verdicts: [{
        ...evalSummary.verdicts[0],
        verdict: 'downgrade',
        gates: [{ name: 'mean_rounds', baseline: 2.4, value: 3.2, delta: 35, threshold: 20, unit: '%', pass: false }],
      }],
    })
    render(EvalResults, { props: { run: RUN, agent: AGENT, quick: true } })

    await waitFor(() => expect(screen.getByTestId('verdict-4')).toBeInTheDocument())
    expect(screen.queryByTestId('escalate-4')).not.toBeInTheDocument()
  })
})

describe('EvalResults — objective table', () => {
  test('lays variants out as columns with a completeness line', async () => {
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('objective-table')).toBeInTheDocument())
    const table = screen.getByTestId('objective-table')
    expect(table).toHaveTextContent('current')
    expect(table).toHaveTextContent('anthropic/claude-3-opus')
    expect(table).toHaveTextContent('Rounds per test case')
    expect(table).toHaveTextContent('2.40')
    expect(table).toHaveTextContent('2.20')

    expect(screen.getByTestId('completeness')).toHaveTextContent(
      '73 of 74 turns finished · 37 of 37 comparisons judged · conclusive')
  })

  test('says inconclusive when the run fell under the floor', async () => {
    withSummary({
      completeness: { ...evalSummary.completeness, samples_ok: 20, conclusive: false },
    })
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('completeness'))
      .toHaveTextContent('inconclusive'))
  })

  test('an empty scorecard says so instead of rendering a blank table', async () => {
    withSummary({ variants: [], per_task: [], verdicts: [] })
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('results-none')).toBeInTheDocument())
  })

  test('surfaces a failed summary read with a retry', async () => {
    server.use(
      http.get('/api/v1/eval/runs/:id/summary', () =>
        HttpResponse.json({ error: 'run not found' }, { status: 404 })),
    )
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('results-retry')).toBeInTheDocument())
    expect(screen.getByText('run not found')).toBeInTheDocument()
  })

  test('a pairs failure still leaves the scorecard standing', async () => {
    server.use(
      http.get('/api/v1/eval/runs/:id/pairs', () =>
        HttpResponse.json({ error: 'boom' }, { status: 500 })),
    )
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('objective-table')).toBeInTheDocument())
    expect(screen.queryByTestId('results-retry')).not.toBeInTheDocument()
  })
})

describe('EvalResults — per-task diffs', () => {
  test('expands a row into both transcripts and the judge call', async () => {
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('task-row-11')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('task-row-11'))

    await waitFor(() => expect(screen.getByTestId('turn-11-0')).toBeInTheDocument())
    // Both sides render, and the trace adapter fed the transcript real rows.
    expect(screen.getByText('Filed three follow-ups.')).toBeInTheDocument()
    expect(screen.getByText('Filed three follow-ups and flagged one blocker.')).toBeInTheDocument()
    expect(screen.getAllByText('kv_list').length).toBe(2)
    expect(screen.getByText('SUPPRESSED')).toBeInTheDocument()

    expect(screen.getByTestId('outcome-11-0')).toHaveTextContent('candidate won')
    const judgment = screen.getByTestId('pair-judgment-71')
    expect(judgment).toHaveTextContent('picked anthropic/claude-3-opus')
    expect(judgment).toHaveTextContent('correctness')
    expect(judgment).toHaveTextContent('Caught the blocker the other answer missed.')
    expect(judgment).toHaveTextContent('rubric v1')
  })

  test('collapses again and reports a failed turn instead of an empty column', async () => {
    server.use(
      http.get('/api/v1/eval/runs/:id/samples', () => HttpResponse.json([
        evalSamples[0],
        { ...evalSamples[1], status: 'failed', error: 'provider timeout', response: '', trace: '' },
      ])),
    )
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('task-row-11')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('task-row-11'))
    await waitFor(() => expect(screen.getByTestId('turn-failed-502')).toHaveTextContent('provider timeout'))

    await fireEvent.click(screen.getByTestId('task-row-11'))
    expect(screen.queryByTestId('turn-11-0')).not.toBeInTheDocument()
  })

  test('the row opens from the keyboard, not just the mouse', async () => {
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('task-row-11')).toBeInTheDocument())
    const row = screen.getByTestId('task-row-11')
    expect(row).toHaveAttribute('tabindex', '0')
    await fireEvent.keyDown(row, { key: 'Enter' })

    await waitFor(() => expect(screen.getByTestId('turn-11-0')).toBeInTheDocument())
  })

  test('a cheaper candidate shows the saving, not a rounded-off zero', async () => {
    withSummary({
      per_task: [{
        ...evalSummary.per_task[0],
        variants: [
          evalSummary.per_task[0].variants[0],
          { ...evalSummary.per_task[0].variants[1], delta_cost: -0.0034 },
        ],
      }],
    })
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('task-row-11')).toHaveTextContent('-$0.0034'))
  })

  test('a failed transcript read offers a retry that recovers', async () => {
    let attempts = 0
    server.use(
      http.get('/api/v1/eval/runs/:id/samples', () => {
        attempts++
        return attempts === 1
          ? HttpResponse.json({ error: 'store busy' }, { status: 500 })
          : HttpResponse.json(evalSamples)
      }),
    )
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('task-row-11')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('task-row-11'))
    await waitFor(() => expect(screen.getByTestId('turns-retry')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('turns-retry'))
    await waitFor(() => expect(screen.getByTestId('turn-11-0')).toBeInTheDocument())
  })

  test('a row carries cost, rounds and latency, each with its own delta', async () => {
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    const row = await screen.findByTestId('task-row-11')
    // Current: $0.02, 3 rounds, 5 s. Candidate: cheaper, a round shorter,
    // 900 ms faster — all three deltas signed against the current model.
    expect(row).toHaveTextContent('3.0 rounds')
    expect(row).toHaveTextContent('5.0 s')
    expect(row).toHaveTextContent('2.0 rounds')
    expect(row).toHaveTextContent('4.1 s')
    expect(row).toHaveTextContent('-1.0')
    expect(row).toHaveTextContent('-900 ms')
  })

  test('labels the test case kind rather than showing its slug', async () => {
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    const row = await screen.findByTestId('task-row-11')
    expect(row).toHaveTextContent('Tool-heavy')
    expect(row).not.toHaveTextContent('tool_heavy')
  })

  test('resolves each dimension letter to the model that won it', async () => {
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('task-row-11')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('task-row-11'))

    await waitFor(() => expect(screen.getByTestId('pair-judgment-71')).toBeInTheDocument())
    const dims = [...screen.getByTestId('pair-judgment-71').querySelectorAll('.dimensions li')]
      .map(li => li.textContent.replace(/\s+/g, ' ').trim())
    // The letters are the blinded presentation order, not a model. Both
    // orders named the candidate, so every non-tie dimension resolves to it.
    expect(dims).toEqual([
      'correctness: anthropic/claude-3-opus',
      'tool_use: anthropic/claude-3-opus',
      'tone: tie',
      'correctness: anthropic/claude-3-opus',
    ])
  })

  test('keeps the raw letter when nothing on the item can unblind it', async () => {
    server.use(http.get('/api/v1/eval/runs/:id/pairs', () => HttpResponse.json({
      ...evalPairs,
      pairs: [{
        ...evalPairs.pairs[0],
        outcome: 'tie',
        items: [{
          item_id: 141, presentation_order: 'ab', status: 'judged',
          verdicts: [{
            judge_ident: 'claude-code', winner: 'tie', winner_variant: '',
            dimensions: { correctness: 'a' }, notes: '', rubric_version: 'v1',
            created_at: '2026-08-17T10:00:00Z',
          }],
        }],
      }],
    })))

    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('task-row-11')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('task-row-11'))

    await waitFor(() => expect(screen.getByTestId('pair-judgment-71')).toBeInTheDocument())
    const judgment = screen.getByTestId('pair-judgment-71')
    expect(judgment).toHaveTextContent('called it a tie')
    expect(judgment.querySelector('.dimensions li')).toHaveTextContent('correctness: a')
  })

  test('an unjudged comparison says so rather than showing nothing', async () => {
    server.use(
      http.get('/api/v1/eval/runs/:id/pairs', () => HttpResponse.json({
        ...evalPairs,
        pairs: [{ ...evalPairs.pairs[0], outcome: 'pending', items: [] }],
      })),
    )
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('task-row-11')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('task-row-11'))

    await waitFor(() => expect(screen.getByTestId('pair-unjudged-71')).toBeInTheDocument())
    expect(screen.getByTestId('outcome-11-0')).toHaveTextContent('not judged yet')
  })
})

describe('EvalResults — apply to agent', () => {
  test('confirms with the agent and both models before patching', async () => {
    let patched = null
    server.use(
      http.patch('/api/v1/agents/:name', async ({ request, params }) => {
        patched = { name: params.name, body: await request.json() }
        return HttpResponse.json({ ok: true })
      }),
    )
    let reloaded = 0
    render(EvalResults, { props: { run: RUN, agent: AGENT, onapplied: () => { reloaded++ } } })

    await waitFor(() => expect(screen.getByTestId('apply-4')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('apply-4'))

    const dialog = screen.getByTestId('apply-confirm')
    expect(dialog).toHaveTextContent('default')
    expect(dialog).toHaveTextContent('kimi-k2.6')
    expect(dialog).toHaveTextContent('anthropic/claude-3-opus')
    // Nothing is sent until the operator confirms.
    expect(patched).toBeNull()

    await fireEvent.click(screen.getByTestId('apply-confirm-btn'))
    await waitFor(() => expect(screen.getByTestId('apply-ok')).toBeInTheDocument())
    expect(patched).toEqual({
      name: 'default',
      body: { llm_model: 'anthropic/claude-3-opus', llm_provider: 'openrouter' },
    })
    expect(reloaded).toBe(1)
    // The button cannot fire the same switch twice.
    expect(screen.getByTestId('apply-4')).toBeDisabled()
    expect(screen.getByTestId('apply-4')).toHaveTextContent('Applied')
  })

  test('a failed patch keeps the dialog open with the error', async () => {
    server.use(
      http.patch('/api/v1/agents/:name', () =>
        HttpResponse.json({ error: 'model not available' }, { status: 400 })),
    )
    render(EvalResults, { props: { run: RUN, agent: AGENT } })

    await waitFor(() => expect(screen.getByTestId('apply-4')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('apply-4'))
    await fireEvent.click(screen.getByTestId('apply-confirm-btn'))

    await waitFor(() => expect(screen.getByTestId('apply-error')).toHaveTextContent('model not available'))
    expect(screen.getByTestId('apply-confirm')).toBeInTheDocument()
    expect(screen.queryByTestId('apply-ok')).not.toBeInTheDocument()
  })
})

describe('evalSampleTranscript', () => {
  test('maps a sample onto the dry-run transcript shape', () => {
    const t = evalSampleTranscript(evalSamples[0], 'kimi-k2.6')
    expect(t.model).toBe('kimi-k2.6')
    expect(t.rounds).toBe(3)
    expect(t.cost_usd).toBe(0.02)
    expect(t.duration_ms).toBe(5000)
    expect(t.suppressed_count).toBe(1)
    expect(t.tool_calls).toHaveLength(2)
    expect(t.tool_calls[0]).toMatchObject({
      tool: 'kv_list', round: 1, outcome: 'ok', suppressed: false, duration_ms: 120,
    })
    // outcome "suppressed" becomes the boolean the transcript renders on.
    expect(t.tool_calls[1].suppressed).toBe(true)
  })

  test('an unreadable trace is no trace, not a broken view', () => {
    expect(evalSampleTranscript({ trace: 'not json' }).tool_calls).toEqual([])
    expect(evalSampleTranscript({ trace: '{"not":"an array"}' }).tool_calls).toEqual([])
    expect(evalSampleTranscript({}).tool_calls).toEqual([])
    expect(evalSampleTranscript(null).rounds).toBe(0)
  })
})
