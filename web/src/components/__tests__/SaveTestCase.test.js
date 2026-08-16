import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'

const SaveTestCase = (await import('../SaveTestCase.svelte')).default

// Captures what the component actually POSTs, so the assertions are about the
// request the server would receive rather than about internal state.
let created = []
let addedTasks = []

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  created = []
  addedTasks = []
  server.use(
    http.get('/api/v1/eval/task-sets', () => HttpResponse.json([
      { id: 1, name: 'regression', description: '', task_count: 4 },
      { id: 2, name: 'smoke', description: '', task_count: 1 },
    ])),
    http.post('/api/v1/eval/task-sets', async ({ request }) => {
      const body = await request.json()
      created.push(body)
      return HttpResponse.json({ id: 3, name: body.name, description: '', task_count: 0 }, { status: 201 })
    }),
    http.post('/api/v1/eval/task-sets/:name/tasks', async ({ request, params }) => {
      const body = await request.json()
      addedTasks.push({ set: params.name, body })
      return HttpResponse.json({ id: 10, ...body }, { status: 201 })
    }),
  )
})

const turns = [
  { role: 'user', text: 'first question' },
  { role: 'assistant', text: 'first answer' },
  { role: 'user', text: 'second question' },
  { role: 'assistant', text: 'second answer' },
]

describe('SaveTestCase', () => {
  test('loads existing task sets and preselects the first', async () => {
    render(SaveTestCase, { props: { prompt: 'do the thing' } })

    await waitFor(() => expect(screen.getByLabelText('Test set')).toBeTruthy())
    expect(screen.getByLabelText('Test set').value).toBe('regression')
    expect(screen.getByText(/regression \(4\)/)).toBeTruthy()
  })

  test('saves the prompt with the chosen category and set', async () => {
    render(SaveTestCase, { props: { prompt: 'do the thing', conversationId: 'chan:main' } })

    await waitFor(() => expect(screen.getByLabelText('Test set')).toBeTruthy())
    await fireEvent.change(screen.getByLabelText('Category'), { target: { value: 'tool_heavy' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(addedTasks.length).toBe(1))
    expect(addedTasks[0].set).toBe('regression')
    expect(addedTasks[0].body.prompt).toBe('do the thing')
    expect(addedTasks[0].body.category).toBe('tool_heavy')
    expect(addedTasks[0].body.source_conversation_id).toBe('chan:main')
    // Message ids are stripped from the sessions API, so provenance is partial.
    expect(addedTasks[0].body.source_message_id).toBeNull()
  })

  test('pins the requested number of preceding turns, newest last', async () => {
    render(SaveTestCase, { props: { prompt: 'third question', precedingTurns: turns } })

    await waitFor(() => expect(screen.getByLabelText('Include preceding turns')).toBeTruthy())
    await fireEvent.input(screen.getByLabelText('Include preceding turns'), { target: { value: '2' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(addedTasks.length).toBe(1))
    expect(addedTasks[0].body.pinned_history).toEqual([
      { role: 'user', content: 'second question' },
      { role: 'assistant', content: 'second answer' },
    ])
  })

  test('omits pinned history when no turns are requested', async () => {
    render(SaveTestCase, { props: { prompt: 'standalone', precedingTurns: turns } })

    await waitFor(() => expect(screen.getByText('Save')).toBeTruthy())
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(addedTasks.length).toBe(1))
    expect(addedTasks[0].body.pinned_history).toBeUndefined()
  })

  test('hides the history control when there are no preceding turns', async () => {
    render(SaveTestCase, { props: { prompt: 'first ever', precedingTurns: [] } })

    await waitFor(() => expect(screen.getByLabelText('Test set')).toBeTruthy())
    expect(screen.queryByLabelText('Include preceding turns')).toBeNull()
  })

  test('creates a new set before adding the task', async () => {
    render(SaveTestCase, { props: { prompt: 'do the thing' } })

    await waitFor(() => expect(screen.getByText('New set…')).toBeTruthy())
    await fireEvent.click(screen.getByText('New set…'))
    await fireEvent.input(screen.getByLabelText('Test set'), { target: { value: 'brand new' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(addedTasks.length).toBe(1))
    expect(created).toEqual([{ name: 'brand new' }])
    expect(addedTasks[0].set).toBe('brand new')
  })

  test('disables save until a new set has a name', async () => {
    render(SaveTestCase, { props: { prompt: 'do the thing' } })

    await waitFor(() => expect(screen.getByText('New set…')).toBeTruthy())
    await fireEvent.click(screen.getByText('New set…'))

    expect(screen.getByText('Save').disabled).toBe(true)
    await fireEvent.input(screen.getByLabelText('Test set'), { target: { value: 'named' } })
    await waitFor(() => expect(screen.getByText('Save').disabled).toBe(false))
  })

  test('shows an inline error when saving fails and stays open', async () => {
    server.use(
      http.post('/api/v1/eval/task-sets/:name/tasks', () =>
        HttpResponse.json({ error: 'prompt is required' }, { status: 400 })),
    )
    render(SaveTestCase, { props: { prompt: 'do the thing' } })

    await waitFor(() => expect(screen.getByText('Save')).toBeTruthy())
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(screen.getByRole('alert')).toBeTruthy())
    expect(screen.getByText('Save')).toBeTruthy()
  })

  test('falls back to creating a set when the list cannot be loaded', async () => {
    server.use(
      http.get('/api/v1/eval/task-sets', () =>
        HttpResponse.json({ error: 'nope' }, { status: 500 })),
    )
    render(SaveTestCase, { props: { prompt: 'do the thing' } })

    await waitFor(() => expect(screen.getByRole('alert')).toBeTruthy())
    expect(screen.getByLabelText('Test set').tagName).toBe('INPUT')
  })

  test('confirms the save and then closes', async () => {
    let closed = false
    render(SaveTestCase, { props: { prompt: 'do the thing', onclose: () => { closed = true } } })

    await waitFor(() => expect(screen.getByText('Save')).toBeTruthy())
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(screen.getByRole('status')).toBeTruthy())
    expect(screen.getByRole('status').textContent).toContain('regression')
    await waitFor(() => expect(closed).toBe(true), { timeout: 2500 })
  })

  test('moves focus into the panel when it opens', async () => {
    render(SaveTestCase, { props: { prompt: 'do the thing' } })

    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText('Test set')))
  })

  test('escape closes the panel', async () => {
    let closed = false
    render(SaveTestCase, { props: { prompt: 'do the thing', onclose: () => { closed = true } } })

    await waitFor(() => expect(screen.getByLabelText('Test set')).toBeTruthy())
    await fireEvent.keyDown(screen.getByLabelText('Test set'), { key: 'Escape' })

    expect(closed).toBe(true)
    expect(addedTasks.length).toBe(0)
  })

  test('cancel closes without saving', async () => {
    let closed = false
    render(SaveTestCase, { props: { prompt: 'do the thing', onclose: () => { closed = true } } })

    await waitFor(() => expect(screen.getByText('Cancel')).toBeTruthy())
    await fireEvent.click(screen.getByText('Cancel'))

    expect(closed).toBe(true)
    expect(addedTasks.length).toBe(0)
  })
})
