import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { evalRuns } from '../../test/handlers.js'
import { token, authMode } from '../../store.js'
import { evalProgress } from '../../wsStore.js'
import Evals from '../../pages/Evals.svelte'

const AGENTS = [
  { name: 'default', model: 'kimi-k2.6', provider: 'openrouter', permission_tier: 'autonomous', skill_count: 2, has_tools: true },
  { name: 'helper', model: 'gpt-4o', provider: 'openrouter', permission_tier: 'supervised', skill_count: 0, has_tools: false },
]

/** Strips every eval read down to empty, which is what a fresh install looks like. */
function emptyInstance() {
  server.use(
    http.get('/api/v1/eval/task-sets', () => HttpResponse.json([])),
    http.get('/api/v1/eval/runs', () => HttpResponse.json([])),
  )
}

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  evalProgress.set(new Map())
  server.use(http.get('/api/v1/agents', () => HttpResponse.json(AGENTS)))
})

afterEach(() => {
  vi.useRealTimers()
})

describe('Evals page — empty state', () => {
  test('teaches the loop and offers Chat and import, but not suggest', async () => {
    emptyInstance()
    render(Evals)

    await waitFor(() => expect(screen.getByTestId('evals-empty')).toBeInTheDocument())
    expect(
      screen.getByText(/Save real conversations as test cases, then compare your current model against a candidate on them/)
    ).toBeInTheDocument()
    expect(screen.getByTestId('empty-chat-cta')).toBeInTheDocument()
    expect(screen.getByTestId('empty-import-cta')).toBeInTheDocument()
    // "Suggest from history" ships in a later slice — absent, not disabled.
    expect(screen.queryByText(/Suggest/i)).not.toBeInTheDocument()
  })

  test('hides the launcher and runs list until something exists', async () => {
    emptyInstance()
    render(Evals)

    await waitFor(() => expect(screen.getByTestId('evals-empty')).toBeInTheDocument())
    expect(screen.queryByTestId('launcher')).not.toBeInTheDocument()
  })

  test('import creates the set on the fly and reports what landed', async () => {
    emptyInstance()
    let createdName = ''
    let importedInto = ''
    server.use(
      http.post('/api/v1/eval/task-sets', async ({ request }) => {
        createdName = (await request.json()).name
        return HttpResponse.json({ id: 9, name: createdName, task_count: 0 }, { status: 201 })
      }),
      http.post('/api/v1/eval/task-sets/:name/import', ({ params }) => {
        importedInto = params.name
        return HttpResponse.json({ imported: 4 })
      }),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('evals-empty')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('empty-import-cta'))

    const file = new File(['{"prompt":"hi","category":"chat"}\n'], 'golden-set.jsonl', { type: 'text/plain' })
    const picker = document.querySelector('input[type="file"]')
    Object.defineProperty(picker, 'files', { value: [file] })
    await fireEvent.change(picker)

    await waitFor(() => expect(screen.getByText('Import')).not.toBeDisabled())
    await fireEvent.click(screen.getByText('Import'))

    await waitFor(() => expect(screen.getByText(/Imported 4 test cases into "golden-set"/)).toBeInTheDocument())
    expect(createdName).toBe('golden-set')
    expect(importedInto).toBe('golden-set')
  })
})

describe('Evals page — launcher', () => {
  test('Quick check posts a sampled subset at k=1', async () => {
    let body = null
    server.use(
      http.post('/api/v1/eval/runs', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ id: 3, task_set_id: 1, status: 'pending', k: body.k, cost_cap: 2, cost_spent: 0, variants: [], samples_done: 0, samples_total: 0, active: true }, { status: 201 })
      }),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())

    await fireEvent.input(document.querySelector('.model-selector input'), { target: { value: 'openai/gpt-4o' } })
    await fireEvent.click(screen.getByTestId('preset-quick'))
    await waitFor(() => expect(screen.getByTestId('start-run')).not.toBeDisabled())
    await fireEvent.click(screen.getByTestId('start-run'))

    await waitFor(() => expect(body).not.toBeNull())
    expect(body.sample_tasks).toBe(10)
    expect(body.k).toBe(1)
  })

  test('Full eval runs the whole set at the configured default_k', async () => {
    let body = null
    server.use(
      http.post('/api/v1/eval/runs', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ id: 3, task_set_id: 1, status: 'pending', k: body.k, cost_cap: 2, cost_spent: 0, variants: [], samples_done: 0, samples_total: 0, active: true }, { status: 201 })
      }),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())

    await fireEvent.input(document.querySelector('.model-selector input'), { target: { value: 'openai/gpt-4o' } })
    await fireEvent.click(screen.getByTestId('preset-full'))
    await waitFor(() => expect(screen.getByTestId('start-run')).not.toBeDisabled())
    await fireEvent.click(screen.getByTestId('start-run'))

    await waitFor(() => expect(body).not.toBeNull())
    expect(body.sample_tasks).toBeUndefined()
    // default_k from GET /eval/config.
    expect(body.k).toBe(3)
  })

  test('Start posts the incumbent first, as an empty overlay', async () => {
    let body = null
    server.use(
      http.post('/api/v1/eval/runs', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ id: 3, task_set_id: 1, status: 'pending', k: 1, cost_cap: 2, cost_spent: 0, variants: [], samples_done: 0, samples_total: 0, active: true }, { status: 201 })
      }),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())

    await fireEvent.input(document.querySelector('.model-selector input'), { target: { value: 'openai/gpt-4o' } })
    await waitFor(() => expect(screen.getByTestId('start-run')).not.toBeDisabled())
    await fireEvent.click(screen.getByTestId('start-run'))

    await waitFor(() => expect(body).not.toBeNull())
    expect(body.variants[0]).toEqual({ name: 'current' })
    expect(body.variants[0].llm_model).toBeUndefined()
    expect(body.variants[1].name).toBe('openai/gpt-4o')
    expect(body.variants[1].llm_model).toBe('openai/gpt-4o')
    expect(body.base_agent).toBe('default')
    expect(body.task_set).toBe('golden-set')
  })

  test('shows the estimate range with its basis, and any note', async () => {
    server.use(
      http.post('/api/v1/eval/estimate', () => HttpResponse.json({
        low: 0.12, high: 0.4, currency: 'USD', basis: 'history', tasks: 10, k: 1,
        per_variant: [], note: 'Two candidate tasks had no cost history.',
      })),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())

    await fireEvent.input(document.querySelector('.model-selector input'), { target: { value: 'openai/gpt-4o' } })

    await waitFor(() => {
      expect(screen.getByTestId('estimate')).toHaveTextContent('$0.12')
      expect(screen.getByTestId('estimate')).toHaveTextContent('$0.40')
      expect(screen.getByTestId('estimate')).toHaveTextContent('from history')
    })
    expect(screen.getByTestId('estimate-note')).toHaveTextContent('Two candidate tasks had no cost history.')
  })

  test('list_price basis is labelled as such', async () => {
    server.use(
      http.post('/api/v1/eval/estimate', () => HttpResponse.json({
        low: 0.5, high: 1.2, currency: 'USD', basis: 'list_price', tasks: 10, k: 1, per_variant: [],
      })),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())
    await fireEvent.input(document.querySelector('.model-selector input'), { target: { value: 'openai/gpt-4o' } })

    await waitFor(() => expect(screen.getByTestId('estimate')).toHaveTextContent('list price'))
  })

  test('an unknown basis invents no number — the cap stands alone', async () => {
    server.use(
      http.post('/api/v1/eval/estimate', () => HttpResponse.json({
        low: 0, high: 0, currency: 'USD', basis: 'unknown', tasks: 10, k: 1, per_variant: [],
      })),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())
    await fireEvent.input(document.querySelector('.model-selector input'), { target: { value: 'openai/gpt-4o' } })

    await waitFor(() => expect(screen.getByTestId('cost-cap')).toHaveValue(2))
    await waitFor(() => {
      expect(screen.getByTestId('estimate')).not.toHaveTextContent('~$')
      expect(screen.getByTestId('estimate')).toHaveTextContent('stops cleanly at the cap')
    })
  })

  test('Start stays disabled until a candidate is named, and says why', async () => {
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())
    expect(screen.getByTestId('start-run')).toBeDisabled()
    expect(screen.getByTestId('start-blocker')).toHaveTextContent('Pick a candidate model')
  })

  test('a hand-typed candidate drops the provider captured for another model', async () => {
    let body = null
    server.use(
      http.post('/api/v1/eval/runs', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ id: 3, task_set_id: 1, status: 'pending', k: 1, cost_cap: 2, cost_spent: 0, variants: [], samples_done: 0, samples_total: 0, active: true }, { status: 201 })
      }),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())

    // Pick from the list, which captures the model's provider...
    const input = document.querySelector('.model-selector input')
    await fireEvent.focus(input)
    // The selector only lists models once a provider is chosen.
    await waitFor(() => expect(document.querySelector('.provider-select')).toBeInTheDocument())
    await fireEvent.change(document.querySelector('.provider-select'), { target: { value: 'openrouter' } })
    await waitFor(() => expect(screen.getByText('OpenAI: GPT-4o')).toBeInTheDocument())
    await fireEvent.mouseDown(screen.getByText('OpenAI: GPT-4o'))

    // ...then hand-edit the field to a model that provider was never read for.
    await fireEvent.input(input, { target: { value: 'anthropic/claude-3-opus' } })
    await waitFor(() => expect(screen.getByTestId('start-run')).not.toBeDisabled())
    await fireEvent.click(screen.getByTestId('start-run'))

    await waitFor(() => expect(body).not.toBeNull())
    expect(body.variants[1].llm_model).toBe('anthropic/claude-3-opus')
    expect(body.variants[1].llm_provider).toBeUndefined()
  })

  test('a rejected run surfaces the error inline', async () => {
    server.use(
      http.post('/api/v1/eval/runs', () =>
        HttpResponse.json({ error: 'task set is empty' }, { status: 400 })),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('launcher')).toBeInTheDocument())
    await fireEvent.input(document.querySelector('.model-selector input'), { target: { value: 'openai/gpt-4o' } })
    await waitFor(() => expect(screen.getByTestId('start-run')).not.toBeDisabled())
    await fireEvent.click(screen.getByTestId('start-run'))

    await waitFor(() => expect(screen.getByText('task set is empty')).toBeInTheDocument())
  })
})

describe('Evals page — runs list', () => {
  test('renders status, progress, spend and the test set for each run', async () => {
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('run-1')).toBeInTheDocument())

    // Statuses render as operator words, not the API's own ("capped" especially).
    expect(screen.getByTestId('run-status-1')).toHaveTextContent('running')
    expect(screen.getByTestId('run-status-2')).toHaveTextContent('finished')
    expect(screen.getByTestId('progress-1')).toHaveTextContent('8 / 20')
    expect(screen.getByTestId('run-1')).toHaveTextContent('$0.31 of $2.00')
    expect(screen.getByTestId('run-1')).toHaveTextContent('golden-set')
    expect(screen.getByTestId('run-1')).toHaveTextContent('current vs openai/gpt-4o')
  })

  test('names the subset when fewer than the whole set ran', async () => {
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('run-1')).toBeInTheDocument())
    // Run 1 drew 10 of the set's 37 cases; run 2 used all of them.
    expect(screen.getByTestId('subset-1')).toHaveTextContent('10 of 37 test cases')
    expect(screen.queryByTestId('subset-2')).not.toBeInTheDocument()
  })

  test('a terminal run expands to the results mount point', async () => {
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('results-2')).toBeInTheDocument())
    // An active run offers Stop instead.
    expect(screen.queryByTestId('results-1')).not.toBeInTheDocument()

    await fireEvent.click(screen.getByTestId('results-2'))
    expect(screen.getByTestId('results-panel-2')).toBeInTheDocument()
  })

  test('Stop confirms before cancelling', async () => {
    let stopped = 0
    server.use(
      http.post('/api/v1/eval/runs/:id/stop', () => {
        stopped++
        return HttpResponse.json({ status: 'stopping' })
      }),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('stop-1')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('stop-1'))
    expect(screen.getByTestId('stop-confirm')).toBeInTheDocument()
    expect(stopped).toBe(0)

    await fireEvent.click(screen.getByRole('button', { name: 'Stop run' }))
    await waitFor(() => expect(stopped).toBe(1))
  })

  test('a failed Stop keeps the dialog open and reports inside it', async () => {
    server.use(
      http.post('/api/v1/eval/runs/:id/stop', () =>
        HttpResponse.json({ error: 'run 1 is done and cannot be stopped' }, { status: 409 })),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('stop-1')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('stop-1'))
    await fireEvent.click(screen.getByRole('button', { name: 'Stop run' }))

    await waitFor(() => expect(screen.getByTestId('stop-error')).toHaveTextContent('cannot be stopped'))
    // The run card may be screens away, so closing on failure would read as success.
    expect(screen.getByTestId('stop-confirm')).toBeInTheDocument()
  })

  test('cancelling the confirm leaves the run alone', async () => {
    let stopped = 0
    server.use(
      http.post('/api/v1/eval/runs/:id/stop', () => {
        stopped++
        return HttpResponse.json({ status: 'stopping' })
      }),
    )
    render(Evals)
    await waitFor(() => expect(screen.getByTestId('stop-1')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('stop-1'))
    await fireEvent.click(screen.getByText('Cancel'))

    await waitFor(() => expect(screen.queryByTestId('stop-confirm')).not.toBeInTheDocument())
    expect(stopped).toBe(0)
  })

  test('polls the active run and stops once nothing is live', async () => {
    let detailReads = 0
    server.use(
      http.get('/api/v1/eval/runs/:id', ({ params }) => {
        if (params.id !== '1') return new HttpResponse(null, { status: 404 })
        detailReads++
        // Third read reports the run finished, which should end the polling.
        const done = detailReads >= 3
        return HttpResponse.json({
          id: 1, task_set_id: 1, base_agent: 'default', status: done ? 'done' : 'running',
          k: 1, cost_cap: 2, cost_spent: 0.5, created_at: '2026-08-18T09:00:00Z', task_count: 10,
          variants: [{ id: 1, run_id: 1, name: 'current', overlay: '{}' }],
          samples_done: done ? 20 : 12, samples_total: 20, active: !done,
        })
      }),
    )
    render(Evals)

    // The mount hydrate is read 1; the poll interval supplies the rest.
    await waitFor(() => expect(screen.getByTestId('progress-1')).toHaveTextContent('12 / 20'), { timeout: 3000 })
    await waitFor(() => expect(screen.getByTestId('run-status-1')).toHaveTextContent('finished'), { timeout: 15000 })

    const settled = detailReads
    await new Promise(r => setTimeout(r, 5000))
    expect(detailReads).toBe(settled)
  }, 25000)

  test('an eval_progress frame wakes an immediate re-read', async () => {
    let detailReads = 0
    server.use(
      http.get('/api/v1/eval/runs/:id', ({ params }) => {
        if (params.id !== '1') return new HttpResponse(null, { status: 404 })
        detailReads++
        return HttpResponse.json({
          id: 1, task_set_id: 1, base_agent: 'default', status: 'running',
          k: 1, cost_cap: 2, cost_spent: 0.9, created_at: '2026-08-18T09:00:00Z', task_count: 10,
          variants: [{ id: 1, run_id: 1, name: 'current', overlay: '{}' }],
          samples_done: 17, samples_total: 20, active: true,
        })
      }),
    )
    render(Evals)
    await waitFor(() => expect(detailReads).toBeGreaterThan(0))
    const before = detailReads

    // The frame is a wake-up, not truth: the page re-reads the run rather than
    // rendering the frame's own numbers.
    evalProgress.set(new Map([[1, {
      type: 'eval_progress', run_id: 1, status: 'running',
      samples_done: 15, samples_total: 20, cost_spent: 0.8, cost_cap: 2, eta_seconds: 30,
    }]]))

    await waitFor(() => expect(detailReads).toBeGreaterThan(before))
    await waitFor(() => expect(screen.getByTestId('progress-1')).toHaveTextContent('17 / 20'))
  })
})

describe('Evals page — results panel', () => {
  test('mounts the results view for a finished run', async () => {
    render(Evals)

    await waitFor(() => expect(screen.getByTestId('results-2')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('results-2'))

    await waitFor(() => expect(screen.getByTestId('verdict-4')).toBeInTheDocument())
    expect(screen.getByTestId('objective-table')).toBeInTheDocument()
    expect(screen.getByTestId('per-task-table')).toBeInTheDocument()
  })

  test('"Run full eval" refills the launcher with the same candidate', async () => {
    // Run 2 covered 37 of 37 cases, so make it a subset to earn the CTA.
    server.use(
      http.get('/api/v1/eval/runs/:id', ({ params }) => {
        const run = evalRuns.find(r => String(r.id) === params.id)
        return run ? HttpResponse.json({ ...run, task_count: 10 }) : new HttpResponse(null, { status: 404 })
      }),
    )
    render(Evals)

    await waitFor(() => expect(screen.getByTestId('results-2')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('results-2'))
    await waitFor(() => expect(screen.getByTestId('escalate-4')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('escalate-4'))

    await waitFor(() =>
      expect(screen.getByTestId('preset-hint')).toHaveTextContent(/All \d+ cases/))
  })
})
