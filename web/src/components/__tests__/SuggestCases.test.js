import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { evalSuggestions } from '../../test/handlers.js'
import { token, authMode } from '../../store.js'

const SuggestCases = (await import('../SuggestCases.svelte')).default

// Assertions are about the requests a server would receive, not internal state.
let created = []
let addedTasks = []

const SETS = [
  { id: 1, name: 'golden-set', description: '', task_count: 4 },
  { id: 2, name: 'smoke', description: '', task_count: 1 },
]

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  created = []
  addedTasks = []
  server.use(
    http.post('/api/v1/eval/task-sets', async ({ request }) => {
      const body = await request.json()
      created.push(body)
      return HttpResponse.json({ id: 9, name: body.name, description: '', task_count: 0 }, { status: 201 })
    }),
    http.post('/api/v1/eval/task-sets/:name/tasks', async ({ request, params }) => {
      const body = await request.json()
      addedTasks.push({ set: params.name, body })
      return HttpResponse.json({ id: 10, ...body }, { status: 201 })
    }),
  )
})

/** Waits for the cards to replace the loading line. */
async function renderPanel(props = {}) {
  const r = render(SuggestCases, { sets: SETS, defaultSet: 'golden-set', ...props })
  await waitFor(() => expect(screen.getByTestId('suggest-cards')).toBeInTheDocument())
  return r
}

describe('SuggestCases — listing', () => {
  test('renders prompt, category chip, and a why line per candidate', async () => {
    await renderPanel()

    expect(screen.getByText('Summarise the on-call handover for this week')).toBeInTheDocument()
    expect(screen.getByText('/digest yesterday')).toBeInTheDocument()
    expect(screen.getByText('Tool-heavy')).toBeInTheDocument()
    expect(screen.getByText('Skill command')).toBeInTheDocument()
    // The why line translates the API's signal names into operator words.
    expect(
      screen.getByText(/Why: a tool call was rejected or failed · took three or more tool rounds/)
    ).toBeInTheDocument()
    expect(screen.getByText(/Pins 2 preceding turns as history/)).toBeInTheDocument()
  })

  test('asks the endpoint for the given agent', async () => {
    let seen = null
    server.use(
      http.get('/api/v1/eval/suggest', ({ request }) => {
        seen = new URL(request.url).searchParams.get('agent')
        return HttpResponse.json(evalSuggestions)
      }),
    )
    await renderPanel({ agent: 'helper' })
    expect(seen).toBe('helper')
  })

  test('shows a spinner line while loading', async () => {
    server.use(
      http.get('/api/v1/eval/suggest', async () => {
        await new Promise(r => setTimeout(r, 30))
        return HttpResponse.json(evalSuggestions)
      }),
    )
    render(SuggestCases, { sets: SETS })
    expect(screen.getByTestId('suggest-loading')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('suggest-cards')).toBeInTheDocument())
  })

  test('teaches what makes a candidate when history has nothing to offer', async () => {
    server.use(http.get('/api/v1/eval/suggest', () => HttpResponse.json({ candidates: [] })))
    render(SuggestCases, { sets: SETS })

    await waitFor(() => expect(screen.getByTestId('suggest-empty')).toBeInTheDocument())
    expect(screen.getByText(/a failed tool call, several tool rounds/)).toBeInTheDocument()
  })

  test('surfaces a load failure with a retry that succeeds', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/eval/suggest', () => {
        calls++
        return calls === 1
          ? HttpResponse.json({ error: 'telemetry not available' }, { status: 501 })
          : HttpResponse.json(evalSuggestions)
      }),
    )
    render(SuggestCases, { sets: SETS })

    await waitFor(() => expect(screen.getByTestId('suggest-error')).toBeInTheDocument())
    await fireEvent.click(screen.getByText('Try again'))
    await waitFor(() => expect(screen.getByTestId('suggest-cards')).toBeInTheDocument())
  })
})

describe('SuggestCases — accept', () => {
  test('adds the case with its category, pinned history, and source ids', async () => {
    await renderPanel()

    await fireEvent.click(screen.getByTestId('accept-chan:ops:101'))
    await waitFor(() => expect(addedTasks).toHaveLength(1))

    expect(addedTasks[0].set).toBe('golden-set')
    expect(addedTasks[0].body).toMatchObject({
      prompt: 'Summarise the on-call handover for this week',
      category: 'chat',
      source_conversation_id: 'chan:ops',
      source_message_id: 101,
    })
    expect(addedTasks[0].body.pinned_history).toEqual([
      { role: 'user', content: 'morning' },
      { role: 'assistant', content: 'hi' },
    ])
    expect(screen.getByTestId('suggest-saved')).toHaveTextContent('Added 1 test case')
    // Accepted candidates leave the list.
    expect(screen.queryByText('Summarise the on-call handover for this week')).not.toBeInTheDocument()
  })

  test('omits pinned history when the turn had none', async () => {
    await renderPanel()

    await fireEvent.click(screen.getByTestId('accept-chan:ops:102'))
    await waitFor(() => expect(addedTasks).toHaveLength(1))
    expect(addedTasks[0].body.pinned_history).toBeUndefined()
  })

  test('creates the chosen set on the fly before adding', async () => {
    await renderPanel({ sets: [], defaultSet: '' })

    await fireEvent.input(screen.getByTestId('suggest-new-set'), { target: { value: 'fresh-set' } })
    await fireEvent.click(screen.getByTestId('accept-chan:ops:101'))

    await waitFor(() => expect(addedTasks).toHaveLength(1))
    expect(created).toEqual([{ name: 'fresh-set' }])
    expect(addedTasks[0].set).toBe('fresh-set')
  })

  test('blocks accept until a set is named', async () => {
    await renderPanel({ sets: [], defaultSet: '' })

    expect(screen.getByTestId('suggest-blocker')).toBeInTheDocument()
    expect(screen.getByTestId('accept-chan:ops:101')).toBeDisabled()
    await fireEvent.input(screen.getByTestId('suggest-new-set'), { target: { value: 'x' } })
    expect(screen.getByTestId('accept-chan:ops:101')).not.toBeDisabled()
  })

  test('reports a failed add and keeps the candidate on screen', async () => {
    server.use(
      http.post('/api/v1/eval/task-sets/:name/tasks', () =>
        HttpResponse.json({ error: 'set is full' }, { status: 400 })),
    )
    await renderPanel()

    await fireEvent.click(screen.getByTestId('accept-chan:ops:101'))
    await waitFor(() => expect(screen.getByTestId('suggest-accept-error')).toHaveTextContent('set is full'))
    expect(screen.getByText('Summarise the on-call handover for this week')).toBeInTheDocument()
  })

  test('notifies the parent with the set that grew', async () => {
    const seen = []
    await renderPanel({ onaccepted: name => seen.push(name) })

    await fireEvent.click(screen.getByTestId('accept-chan:ops:101'))
    await waitFor(() => expect(seen).toEqual(['golden-set']))
  })
})

describe('SuggestCases — batch accept and reject', () => {
  test('accepts every selected candidate into one set', async () => {
    await renderPanel()

    await fireEvent.click(screen.getByText('Select all'))
    await fireEvent.click(screen.getByTestId('accept-selected'))

    await waitFor(() => expect(addedTasks).toHaveLength(3))
    expect(addedTasks.every(t => t.set === 'golden-set')).toBe(true)
    await waitFor(() =>
      expect(screen.getByTestId('suggest-saved')).toHaveTextContent('Added 3 test cases to “golden-set”'))
    await waitFor(() => expect(screen.getByTestId('suggest-empty')).toBeInTheDocument())
  })

  test('the batch button counts the selection and is disabled while empty', async () => {
    await renderPanel()

    const btn = screen.getByTestId('accept-selected')
    expect(btn).toBeDisabled()
    expect(btn).toHaveTextContent('Accept selected (0)')

    const boxes = screen.getAllByRole('checkbox')
    await fireEvent.click(boxes[0])
    expect(btn).not.toBeDisabled()
    expect(btn).toHaveTextContent('Accept selected (1)')
  })

  test('a mid-batch failure says how many landed', async () => {
    let n = 0
    server.use(
      http.post('/api/v1/eval/task-sets/:name/tasks', async ({ request }) => {
        n++
        if (n === 2) return HttpResponse.json({ error: 'store is busy' }, { status: 500 })
        const body = await request.json()
        return HttpResponse.json({ id: n, ...body }, { status: 201 })
      }),
    )
    await renderPanel()

    await fireEvent.click(screen.getByText('Select all'))
    await fireEvent.click(screen.getByTestId('accept-selected'))

    await waitFor(() =>
      expect(screen.getByTestId('suggest-accept-error')).toHaveTextContent('Added 1 of 3, then failed'))
  })

  test('reject hides the candidate without writing anything', async () => {
    await renderPanel()

    await fireEvent.click(screen.getByTestId('reject-chan:ops:102'))
    expect(screen.queryByText('/digest yesterday')).not.toBeInTheDocument()
    expect(addedTasks).toHaveLength(0)
    // The other candidates stay.
    expect(screen.getByText('Summarise the on-call handover for this week')).toBeInTheDocument()
  })

  test('refresh brings rejected candidates back', async () => {
    await renderPanel()

    await fireEvent.click(screen.getByTestId('reject-chan:ops:102'))
    expect(screen.queryByText('/digest yesterday')).not.toBeInTheDocument()

    await fireEvent.click(screen.getByTestId('suggest-refresh'))
    await waitFor(() => expect(screen.getByText('/digest yesterday')).toBeInTheDocument())
  })

  test('rejecting everything says the list is exhausted, not that history is empty', async () => {
    await renderPanel()

    for (const id of ['chan:ops:101', 'chan:ops:102', 'chan:dev:103']) {
      await fireEvent.click(screen.getByTestId(`reject-${id}`))
    }
    await waitFor(() => expect(screen.getByTestId('suggest-empty')).toBeInTheDocument())
    expect(screen.getByText(/All suggestions handled/)).toBeInTheDocument()
  })
})
