import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { traceList, traceRows, traceDetail } from '../../test/handlers.js'
import { token, authMode } from '../../store.js'
import Traces from '../../pages/Traces.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

/** An instance where capture has never been turned on. */
function captureOff() {
  server.use(
    http.get('/api/v1/traces', () =>
      HttpResponse.json({ traces: [], total: 0, limit: 50, offset: 0, capture: false, retention_days: 30 })),
  )
}

describe('Turn inspector — empty states', () => {
  test('tells the operator capture is off and how to turn it on', async () => {
    captureOff()
    render(Traces)

    await waitFor(() => expect(screen.getByTestId('traces-capture-off')).toBeInTheDocument())
    expect(screen.getByText(/Live turn capture is off/)).toBeInTheDocument()
    expect(screen.getByText(/capture = true/)).toBeInTheDocument()
    // The reason for the default belongs in the copy, not in a doc nobody opens.
    expect(screen.getByText(/most sensitive data/)).toBeInTheDocument()
    expect(screen.queryByTestId('trace-rows')).not.toBeInTheDocument()
  })

  test('with capture on but nothing recorded, says to wait rather than to configure', async () => {
    server.use(
      http.get('/api/v1/traces', () =>
        HttpResponse.json({ traces: [], total: 0, limit: 50, offset: 0, capture: true, retention_days: 30 })),
    )
    render(Traces)

    await waitFor(() => expect(screen.getByTestId('traces-empty')).toBeInTheDocument())
    expect(screen.getByText(/No turns recorded yet/)).toBeInTheDocument()
    expect(screen.queryByTestId('traces-capture-off')).not.toBeInTheDocument()
  })

  test('shows a loading state before the first response', async () => {
    server.use(
      http.get('/api/v1/traces', async () => {
        await new Promise(r => setTimeout(r, 50))
        return HttpResponse.json(traceList)
      }),
    )
    render(Traces)
    expect(screen.getByTestId('traces-loading')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
  })

  test('surfaces a failed list read without diagnosing it as a config problem', async () => {
    server.use(
      http.get('/api/v1/traces', () => HttpResponse.json({ error: 'turn traces not configured' }, { status: 503 })),
    )
    render(Traces)

    await waitFor(() => expect(screen.getByText(/turn traces not configured/)).toBeInTheDocument())
    // A failed read leaves the list empty and capture false, which must not be
    // reported as "capture is off" — the operator would edit a correct config.
    expect(screen.queryByTestId('traces-capture-off')).not.toBeInTheDocument()
    expect(screen.queryByTestId('traces-empty')).not.toBeInTheDocument()
  })
})

describe('Turn inspector — list', () => {
  test('lists recorded turns with their source, model and usage', async () => {
    render(Traces)

    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
    const rows = screen.getAllByTestId('trace-row')
    expect(rows).toHaveLength(traceRows.length)
    expect(rows[0]).toHaveTextContent('live')
    expect(rows[0]).toHaveTextContent('default')
    expect(rows[0]).toHaveTextContent('claude-opus-5')
    expect(rows[0]).toHaveTextContent('1 round')
    expect(rows[0]).toHaveTextContent('1020 tok')
  })

  test('marks a trace the byte cap trimmed', async () => {
    render(Traces)

    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
    const rows = screen.getAllByTestId('trace-row')
    expect(rows[1]).toHaveTextContent('TRIMMED')
    expect(rows[0]).not.toHaveTextContent('TRIMMED')
  })

  test('says capture is on in the header', async () => {
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('capture-on')).toHaveTextContent(/kept 30 days/))
  })

  // Eval traces on screen with live capture off looks identical to a working
  // recorder unless the header says otherwise.
  test('says live capture is off even when eval traces are listed', async () => {
    server.use(
      http.get('/api/v1/traces', () => HttpResponse.json({ ...traceList, capture: false })),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('capture-off-note'))
      .toHaveTextContent(/eval samples only/))
    expect(screen.getAllByTestId('trace-row').length).toBeGreaterThan(0)
  })

  test('keeps the filter bar mounted while a filtered read is in flight', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/traces', async () => {
        calls++
        if (calls > 1) await new Promise(r => setTimeout(r, 40))
        return HttpResponse.json(traceList)
      }),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('source-live'))
    // FilterChips owns a roving-tabindex radiogroup; unmounting it mid-refetch
    // throws keyboard focus to the body.
    expect(screen.getByTestId('source-filter')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
  })

  test('filtering by source re-reads the list with the filter', async () => {
    const seen = []
    server.use(
      http.get('/api/v1/traces', ({ request }) => {
        const url = new URL(request.url)
        seen.push(url.searchParams.get('source'))
        return HttpResponse.json(traceList)
      }),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('source-eval'))
    await waitFor(() => expect(seen).toContain('eval'))
  })

  test('a filter that matches nothing offers a way back', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/traces', () => {
        calls++
        if (calls === 1) return HttpResponse.json(traceList)
        return HttpResponse.json({ traces: [], total: 0, limit: 50, offset: 0, capture: true, retention_days: 30 })
      }),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('source-eval'))
    await waitFor(() => expect(screen.getByTestId('traces-empty')).toBeInTheDocument())
    expect(screen.getByText(/No turns recorded match this filter/)).toBeInTheDocument()
    expect(screen.getByTestId('clear-filters')).toBeInTheDocument()
  })
})

describe('Turn inspector — rows', () => {
  test('offers no dry-run source filter', async () => {
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
    // A preview's trace rides out on its response and is never stored, so the
    // chip would only ever return an empty list.
    expect(screen.queryByTestId('source-dryrun')).not.toBeInTheDocument()
  })

  test('renders a zero-millisecond turn as a duration, not a dash', async () => {
    server.use(
      http.get('/api/v1/traces', () => HttpResponse.json({
        ...traceList,
        traces: [{ ...traceRows[0], latency_ms: 0 }],
        total: 1,
      })),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
    expect(screen.getByText(/0 ms/)).toBeInTheDocument()
  })
})

describe('Turn inspector — detail', () => {
  test('expands inline and shows the prompt, system prompt, history and tool payloads', async () => {
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())

    // The message that started the turn is always visible; the final response
    // comes from the shared transcript renderer.
    await fireEvent.click(screen.getAllByTestId('trace-row')[0])
    await waitFor(() => expect(screen.getByText('and today?')).toBeInTheDocument())
    expect(screen.getByText('Today I fetched the calendar.')).toBeInTheDocument()
    expect(screen.getByText('web_fetch')).toBeInTheDocument()
    expect(screen.getByText('SUPPRESSED')).toBeInTheDocument()

    // The long panels are collapsed until asked for: the system prompt is
    // thousands of characters and would bury the turn.
    expect(screen.queryByTestId('trace-system-prompt')).not.toBeInTheDocument()
    await fireEvent.click(screen.getByRole('button', { name: /System prompt as sent/ }))
    await waitFor(() => expect(screen.getByTestId('trace-system-prompt')).toHaveTextContent('You are the default agent.'))

    await fireEvent.click(screen.getByRole('button', { name: /History window as sent/ }))
    await waitFor(() => expect(screen.getByTestId('trace-history')).toHaveTextContent('what did you do yesterday'))
  })

  test('collapses again on a second click and keeps one row open at a time', async () => {
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
    const rows = screen.getAllByTestId('trace-row')

    await fireEvent.click(rows[0])
    await waitFor(() => expect(screen.getByTestId('trace-detail')).toBeInTheDocument())

    await fireEvent.click(rows[1])
    await waitFor(() => expect(screen.getAllByTestId('trace-detail')).toHaveLength(1))

    await fireEvent.click(rows[1])
    await waitFor(() => expect(screen.queryByTestId('trace-detail')).not.toBeInTheDocument())
  })

  test('reports what truncation removed', async () => {
    server.use(
      http.get('/api/v1/traces/:id', () => HttpResponse.json({
        ...traceDetail,
        ...traceRows[1],
        truncation: { dropped_rounds: 4, note: 'trace exceeded 262144 bytes: 4 oldest round(s) dropped' },
      })),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())

    await fireEvent.click(screen.getAllByTestId('trace-row')[1])
    await waitFor(() => expect(screen.getByTestId('trace-truncation'))
      .toHaveTextContent('4 oldest round(s) dropped'))
  })

  test('a turn with no preceding context says so', async () => {
    server.use(
      http.get('/api/v1/traces/:id', () => HttpResponse.json({ ...traceDetail, ...traceRows[0], history: [] })),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())

    await fireEvent.click(screen.getAllByTestId('trace-row')[0])
    await waitFor(() => expect(screen.getByRole('button', { name: /History window as sent/ })).toBeInTheDocument())
    await fireEvent.click(screen.getByRole('button', { name: /History window as sent/ }))
    await waitFor(() => expect(screen.getByTestId('trace-history-empty')).toBeInTheDocument())
  })

  test('ignores a detail response for a row that is no longer open', async () => {
    server.use(
      http.get('/api/v1/traces/:id', async ({ params }) => {
        // The first row's read is the slow one, so it lands after the second.
        if (params.id === '2') await new Promise(r => setTimeout(r, 60))
        return HttpResponse.json({
          ...traceDetail,
          prompt: params.id === '2' ? 'first row prompt' : 'second row prompt',
        })
      }),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
    const rows = screen.getAllByTestId('trace-row')

    await fireEvent.click(rows[0])
    await fireEvent.click(rows[1])
    await waitFor(() => expect(screen.getByText('second row prompt')).toBeInTheDocument())

    // Long enough for the first row's slower response to arrive.
    await new Promise(r => setTimeout(r, 120))
    expect(screen.queryByText('first row prompt')).not.toBeInTheDocument()
    expect(screen.getByText('second row prompt')).toBeInTheDocument()
  })

  test('surfaces a failed detail read without closing the row', async () => {
    server.use(
      http.get('/api/v1/traces/:id', () => HttpResponse.json({ error: 'turn trace 2: eval: not found' }, { status: 404 })),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())

    await fireEvent.click(screen.getAllByTestId('trace-row')[0])
    await waitFor(() => expect(screen.getByTestId('detail-error')).toHaveTextContent(/not found/))
  })
})

describe('Turn inspector — paging', () => {
  test('loads older turns when there are more than the page', async () => {
    const older = { ...traceRows[1], id: 0, agent: 'archivist' }
    let calls = 0
    server.use(
      http.get('/api/v1/traces', () => {
        calls++
        if (calls === 1) return HttpResponse.json({ ...traceList, total: 3 })
        return HttpResponse.json({ traces: [older], total: 3, limit: 50, offset: 50, capture: true })
      }),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('load-more')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('load-more'))
    await waitFor(() => expect(screen.getAllByTestId('trace-row')).toHaveLength(3))
  })

  test('drops rows a shifted page repeats instead of duplicating keys', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/traces', () => {
        calls++
        if (calls === 1) return HttpResponse.json({ ...traceList, total: 3 })
        // A turn recorded between the two reads pushes row 1 onto page two.
        return HttpResponse.json({
          traces: [traceRows[1], { ...traceRows[1], id: 0, agent: 'archivist' }],
          total: 3, limit: 50, offset: 50, capture: true,
        })
      }),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('load-more')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('load-more'))
    await waitFor(() => expect(screen.getAllByTestId('trace-row')).toHaveLength(3))
    expect(screen.getByText('archivist')).toBeInTheDocument()
  })

  test('drops a late older-page read after the filter has changed', async () => {
    server.use(
      http.get('/api/v1/traces', async ({ request }) => {
        const url = new URL(request.url)
        // The older page is the slow read; the operator abandons it by
        // switching the source filter while it is still in flight.
        if (url.searchParams.get('offset') === '50') {
          await new Promise(r => setTimeout(r, 60))
          return HttpResponse.json({
            traces: [{ ...traceRows[1], id: 99, agent: 'stale-agent' }],
            total: 3, limit: 50, offset: 50, capture: true,
          })
        }
        if (url.searchParams.get('source') === 'eval') {
          return HttpResponse.json({ traces: [traceRows[1]], total: 1, limit: 50, offset: 0, capture: true })
        }
        return HttpResponse.json({ ...traceList, total: 3 })
      }),
    )
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('load-more')).toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('load-more'))
    await fireEvent.click(screen.getByTestId('source-eval'))
    await waitFor(() => expect(screen.getAllByTestId('trace-row')).toHaveLength(1))

    // Long enough for the abandoned page to land; it must not be appended to
    // the filtered list it no longer belongs to.
    await new Promise(r => setTimeout(r, 120))
    expect(screen.queryByText('stale-agent')).not.toBeInTheDocument()
    expect(screen.getAllByTestId('trace-row')).toHaveLength(1)
  })

  test('hides the load-more control when everything is shown', async () => {
    render(Traces)
    await waitFor(() => expect(screen.getByTestId('trace-rows')).toBeInTheDocument())
    expect(screen.queryByTestId('load-more')).not.toBeInTheDocument()
  })
})
