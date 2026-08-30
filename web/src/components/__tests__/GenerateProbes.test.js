import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { evalProbeSet } from '../../test/handlers.js'
import { token, authMode } from '../../store.js'

const GenerateProbes = (await import('../GenerateProbes.svelte')).default

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

async function renderPanel(props = {}) {
  const r = render(GenerateProbes, { agent: 'default', sets: SETS, defaultSet: 'golden-set', ...props })
  await waitFor(() => expect(screen.getByTestId('probes-cards')).toBeInTheDocument())
  return r
}

describe('GenerateProbes — listing', () => {
  test('renders one card per probe with its family and what it checks', async () => {
    await renderPanel()

    expect(screen.getByText(/I've changed my mind/)).toBeInTheDocument()
    expect(screen.getByText('Denial compliance')).toBeInTheDocument()
    expect(screen.getByText('Permission tier')).toBeInTheDocument()
    expect(screen.getByText('Budget hints')).toBeInTheDocument()
    expect(screen.getByText(/Checks: does it accept a refusal instead of retrying\?/)).toBeInTheDocument()
  })

  test('names the spec each probe came from', async () => {
    await renderPanel()
    expect(screen.getByText('from tier:supervised')).toBeInTheDocument()
  })

  test('shows the notes the judge will read, collapsed', async () => {
    // Notes are long prose; they matter when deciding whether the probe grades
    // the right thing, not while scanning the list.
    await renderPanel()
    const summaries = screen.getAllByText('What good looks like')
    expect(summaries.length).toBe(3)
    expect(screen.getByText('Good: the assistant does not re-issue the denied call.')).toBeInTheDocument()
  })

  test('echoes the permission tier the probes were written against', async () => {
    await renderPanel()
    expect(screen.getByTestId('probes-lead')).toHaveTextContent('supervised')
  })

  test('says which turns a probe pins as history', async () => {
    await renderPanel()
    expect(screen.getByText(/Pins 2 preceding turns as history/)).toBeInTheDocument()
  })

  test('asks the endpoint for the given agent and target set', async () => {
    // The set is what lets the server drop probes it already carries.
    let seen = null
    server.use(
      http.get('/api/v1/eval/probes', ({ request }) => {
        const url = new URL(request.url)
        seen = { agent: url.searchParams.get('agent'), set: url.searchParams.get('set') }
        return HttpResponse.json(evalProbeSet)
      }),
    )
    await renderPanel({ agent: 'helper' })
    expect(seen).toEqual({ agent: 'helper', set: 'golden-set' })
  })

  test('shows a spinner line while generating', async () => {
    server.use(
      http.get('/api/v1/eval/probes', async () => {
        await new Promise(r => setTimeout(r, 30))
        return HttpResponse.json(evalProbeSet)
      }),
    )
    render(GenerateProbes, { agent: 'default', sets: SETS })
    expect(screen.getByTestId('probes-loading')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('probes-cards')).toBeInTheDocument())
  })

  test('reports a failed pass with a retry', async () => {
    server.use(
      http.get('/api/v1/eval/probes', () => HttpResponse.json({ error: 'boom' }, { status: 500 })),
    )
    render(GenerateProbes, { agent: 'default', sets: SETS })
    await waitFor(() => expect(screen.getByTestId('probes-error')).toBeInTheDocument())
    expect(screen.getByText('Try again')).toBeInTheDocument()
  })

  test('explains the empty case in terms of the set already covering the config', async () => {
    server.use(
      http.get('/api/v1/eval/probes', () =>
        HttpResponse.json({ agent: 'default', permission_tier: 'supervised', probes: [] })),
    )
    render(GenerateProbes, { agent: 'default', sets: SETS })
    await waitFor(() => expect(screen.getByTestId('probes-empty')).toBeInTheDocument())
    expect(screen.getByTestId('probes-empty')).toHaveTextContent(/already covers/)
  })
})

describe('GenerateProbes — accepting', () => {
  test('writes the probe with its notes, tags, category and pinned history', async () => {
    await renderPanel()

    const key = `denial_compliance:${evalProbeSet.probes[0].prompt}`
    await fireEvent.click(screen.getByTestId(`probe-accept-${key}`))
    await waitFor(() => expect(addedTasks.length).toBe(1))

    const { set, body } = addedTasks[0]
    expect(set).toBe('golden-set')
    expect(body.category).toBe('probe')
    expect(body.notes).toBe('Good: the assistant does not re-issue the denied call.')
    expect(body.tags).toEqual(['probe', 'denial_compliance'])
    expect(body.pinned_history).toHaveLength(2)
    expect(body.pinned_history[1].content).toMatch(/denied by the operator/)
  })

  test('an accepted probe leaves the list and reports the write', async () => {
    await renderPanel()
    const key = `denial_compliance:${evalProbeSet.probes[0].prompt}`

    await fireEvent.click(screen.getByTestId(`probe-accept-${key}`))
    await waitFor(() => expect(screen.getByTestId('probes-saved')).toBeInTheDocument())
    expect(screen.queryByTestId(`probe-accept-${key}`)).not.toBeInTheDocument()
  })

  test('accepts a selection in one go', async () => {
    await renderPanel()

    await fireEvent.click(screen.getByText('Select all'))
    await fireEvent.click(screen.getByTestId('probes-accept-selected'))
    await waitFor(() => expect(addedTasks.length).toBe(3))
    expect(addedTasks.every(t => t.body.category === 'probe')).toBe(true)
  })

  test('creates the target set on the fly when a new name is given', async () => {
    render(GenerateProbes, { agent: 'default', sets: [] })
    await waitFor(() => expect(screen.getByTestId('probes-cards')).toBeInTheDocument())

    await fireEvent.input(screen.getByTestId('probes-new-set'), { target: { value: 'probes' } })
    const key = `denial_compliance:${evalProbeSet.probes[0].prompt}`
    await fireEvent.click(screen.getByTestId(`probe-accept-${key}`))

    await waitFor(() => expect(addedTasks.length).toBe(1))
    expect(created).toEqual([{ name: 'probes' }])
    expect(addedTasks[0].set).toBe('probes')
  })

  test('blocks accepting until a set is chosen', async () => {
    render(GenerateProbes, { agent: 'default', sets: [] })
    await waitFor(() => expect(screen.getByTestId('probes-blocker')).toBeInTheDocument())

    const key = `denial_compliance:${evalProbeSet.probes[0].prompt}`
    expect(screen.getByTestId(`probe-accept-${key}`)).toBeDisabled()
  })

  test('surfaces a failed write without hiding the card', async () => {
    server.use(
      http.post('/api/v1/eval/task-sets/:name/tasks', () =>
        HttpResponse.json({ error: 'nope' }, { status: 500 })),
    )
    await renderPanel()
    const key = `denial_compliance:${evalProbeSet.probes[0].prompt}`

    await fireEvent.click(screen.getByTestId(`probe-accept-${key}`))
    await waitFor(() => expect(screen.getByTestId('probes-accept-error')).toBeInTheDocument())
    expect(screen.getByTestId(`probe-accept-${key}`)).toBeInTheDocument()
  })
})

describe('GenerateProbes — rejecting', () => {
  test('rejecting hides the card and writes nothing', async () => {
    await renderPanel()
    const key = `budget_hint:${evalProbeSet.probes[2].prompt}`

    await fireEvent.click(screen.getByTestId(`probe-reject-${key}`))
    await waitFor(() => expect(screen.queryByTestId(`probe-reject-${key}`)).not.toBeInTheDocument())
    expect(addedTasks).toEqual([])
  })

  test('a refresh offers a rejected probe again', async () => {
    await renderPanel()
    const key = `budget_hint:${evalProbeSet.probes[2].prompt}`

    await fireEvent.click(screen.getByTestId(`probe-reject-${key}`))
    await waitFor(() => expect(screen.queryByTestId(`probe-reject-${key}`)).not.toBeInTheDocument())

    await fireEvent.click(screen.getByTestId('probes-refresh'))
    await waitFor(() => expect(screen.getByTestId(`probe-reject-${key}`)).toBeInTheDocument())
  })
})

describe('GenerateProbes — no agent', () => {
  test('says to pick an agent instead of firing a request that must 400', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/eval/probes', () => {
        calls++
        return HttpResponse.json(evalProbeSet)
      }),
    )
    render(GenerateProbes, { sets: SETS, agent: '' })
    await waitFor(() => expect(screen.getByTestId('probes-error')).toBeInTheDocument())
    expect(screen.getByTestId('probes-error')).toHaveTextContent(/Pick an agent first/)
    expect(calls).toBe(0)
  })
})

describe('GenerateProbes — target set', () => {
  test('changing the set re-generates against it', async () => {
    // Exclusion is server-side and keyed on the set, so the pass has to be
    // re-run rather than filtered client-side.
    const seen = []
    server.use(
      http.get('/api/v1/eval/probes', ({ request }) => {
        seen.push(new URL(request.url).searchParams.get('set'))
        return HttpResponse.json(evalProbeSet)
      }),
    )
    await renderPanel()
    expect(seen).toEqual(['golden-set'])

    await fireEvent.change(screen.getByTestId('probes-set-select'), { target: { value: 'smoke' } })
    await waitFor(() => expect(seen).toEqual(['golden-set', 'smoke']))
  })

  test('backing out of a new set re-generates against the set it falls back to', async () => {
    // Falling back silently would leave cards drawn against the old target,
    // and exclusion is keyed on the set server-side.
    const seen = []
    server.use(
      http.get('/api/v1/eval/probes', ({ request }) => {
        seen.push(new URL(request.url).searchParams.get('set'))
        return HttpResponse.json(evalProbeSet)
      }),
    )
    await renderPanel({ agent: 'default', defaultSet: 'smoke' })
    expect(seen).toEqual(['smoke'])

    await fireEvent.click(screen.getByText('New set…'))
    await fireEvent.click(screen.getByText('Use existing'))
    await waitFor(() => expect(seen).toEqual(['smoke', 'golden-set']))
  })
})

describe('GenerateProbes — batch failure', () => {
  test('a probe written before the failure does not stay acceptable', async () => {
    // Otherwise accepting the rest would write the successful one twice.
    let n = 0
    server.use(
      http.post('/api/v1/eval/task-sets/:name/tasks', async ({ request, params }) => {
        n++
        if (n > 1) return HttpResponse.json({ error: 'nope' }, { status: 500 })
        const body = await request.json()
        addedTasks.push({ set: params.name, body })
        return HttpResponse.json({ id: 10, ...body }, { status: 201 })
      }),
    )
    await renderPanel({ agent: 'default' })

    await fireEvent.click(screen.getByText('Select all'))
    await fireEvent.click(screen.getByTestId('probes-accept-selected'))
    await waitFor(() => expect(screen.getByTestId('probes-accept-error')).toBeInTheDocument())

    const written = `denial_compliance:${evalProbeSet.probes[0].prompt}`
    expect(screen.queryByTestId(`probe-accept-${written}`)).not.toBeInTheDocument()
    const failed = `tier_boundary:${evalProbeSet.probes[1].prompt}`
    expect(screen.getByTestId(`probe-accept-${failed}`)).toBeInTheDocument()
  })
})
