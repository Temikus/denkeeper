import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { evalTaskSets } from '../../test/handlers.js'
import { api } from '../../api.js'
import { token, authMode } from '../../store.js'

const EvalTestSets = (await import('../EvalTestSets.svelte')).default

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

afterEach(() => {
  vi.restoreAllMocks()
})

/** Renders golden-set and waits for its cases. */
async function renderSets(props = {}) {
  const r = render(EvalTestSets, { sets: evalTaskSets, selected: 'golden-set', ...props })
  await waitFor(() => expect(screen.getByTestId('cases-table')).toBeInTheDocument())
  return r
}

describe('EvalTestSets — reading', () => {
  test('lists the open set\'s cases with their kind, notes and pinned turns', async () => {
    await renderSets()

    expect(screen.getByTestId('case-101')).toHaveTextContent('What is on my calendar tomorrow?')
    expect(screen.getByTestId('case-101')).toHaveTextContent('Chat / persona')
    expect(screen.getByTestId('case-102')).toHaveTextContent('Needs kv + web_fetch')
    expect(screen.getByTestId('case-102')).toHaveTextContent('2 pinned turns before it')
    expect(screen.getByTestId('coverage')).toBeInTheDocument()
  })

  test('an empty set says how to add cases', async () => {
    render(EvalTestSets, { sets: evalTaskSets, selected: 'tool-heavy' })
    await waitFor(() => expect(screen.getByTestId('sets-empty')).toBeInTheDocument())
    expect(screen.queryByTestId('cases-table')).not.toBeInTheDocument()
  })

  test('a failed read shows the error with a retry', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/eval/task-sets/:name', () => {
        calls++
        return calls === 1
          ? HttpResponse.json({ error: 'store unavailable' }, { status: 500 })
          : HttpResponse.json({ ...evalTaskSets[0], tasks: [] })
      }),
    )
    render(EvalTestSets, { sets: evalTaskSets, selected: 'golden-set' })

    await waitFor(() => expect(screen.getByTestId('sets-error')).toHaveTextContent('store unavailable'))
    await fireEvent.click(screen.getByText('Try again'))
    await waitFor(() => expect(screen.getByTestId('sets-empty')).toBeInTheDocument())
  })
})

describe('EvalTestSets — editing', () => {
  test('an inline edit PATCHes kind and notes and shows the saved values', async () => {
    let body = null
    server.use(
      http.patch('/api/v1/eval/task-sets/:name/tasks/:id', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ id: 101, set_id: 1, prompt: 'What is on my calendar tomorrow?', ...body })
      }),
    )
    await renderSets()

    await fireEvent.click(screen.getByTestId('edit-case-101'))
    await fireEvent.change(screen.getByTestId('edit-kind-101'), { target: { value: 'tool_heavy' } })
    await fireEvent.input(screen.getByTestId('edit-notes-101'), { target: { value: 'reads the calendar tool' } })
    await fireEvent.click(screen.getByTestId('save-case-101'))

    await waitFor(() => expect(screen.getByTestId('case-101')).toHaveTextContent('reads the calendar tool'))
    expect(body).toEqual({ category: 'tool_heavy', notes: 'reads the calendar tool' })
    expect(screen.getByTestId('case-101')).toHaveTextContent('Tool-heavy')
    expect(screen.getByTestId('case-101')).toHaveTextContent('Saved')
  })

  test('editing moves focus into the row and back to Edit on cancel', async () => {
    await renderSets()

    await fireEvent.click(screen.getByTestId('edit-case-101'))
    expect(document.activeElement).toBe(screen.getByTestId('edit-kind-101'))
    await fireEvent.click(screen.getByText('Cancel'))
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('edit-case-101')))
  })

  test('a save that lands after switching sets does not touch the new set', async () => {
    let release
    const update = vi.spyOn(api, 'updateEvalTask')
    server.use(
      http.patch('/api/v1/eval/task-sets/:name/tasks/:id', async () => {
        await new Promise(r => { release = r })
        return HttpResponse.json({ id: 101, set_id: 1, prompt: 'x', category: 'chat', notes: 'late' })
      }),
    )
    await renderSets()

    await fireEvent.click(screen.getByTestId('edit-case-101'))
    await fireEvent.click(screen.getByTestId('save-case-101'))
    await waitFor(() => expect(release).toBeTypeOf('function'))
    await fireEvent.change(screen.getByTestId('sets-select'), { target: { value: 'tool-heavy' } })
    await waitFor(() => expect(screen.getByTestId('sets-empty')).toBeInTheDocument())

    release()
    // The component's continuation was registered first, so it has run once
    // this settles; tick flushes what it wrote.
    await update.mock.results[0].value
    await tick()
    expect(screen.getByTestId('sets-empty')).toBeInTheDocument()
    expect(screen.queryByText('late')).not.toBeInTheDocument()
    // Nor announce a save for a row that is no longer on screen.
    expect(screen.queryByText('Case saved')).not.toBeInTheDocument()
  })

  test('a failed save keeps the row open with the error', async () => {
    server.use(
      http.patch('/api/v1/eval/task-sets/:name/tasks/:id', () =>
        HttpResponse.json({ error: 'invalid category' }, { status: 400 })),
    )
    await renderSets()

    await fireEvent.click(screen.getByTestId('edit-case-101'))
    await fireEvent.click(screen.getByTestId('save-case-101'))

    await waitFor(() => expect(screen.getByTestId('edit-error')).toHaveTextContent('invalid category'))
    expect(screen.getByTestId('edit-notes-101')).toBeInTheDocument()
  })
})

describe('EvalTestSets — deleting', () => {
  test('Delete case confirms, DELETEs and drops the row', async () => {
    let deleted = null
    server.use(
      http.delete('/api/v1/eval/task-sets/:name/tasks/:id', ({ params }) => {
        deleted = `${params.name}/${params.id}`
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const onchanged = vi.fn()
    await renderSets({ onchanged })

    await fireEvent.click(screen.getByTestId('delete-case-101'))
    expect(screen.getByTestId('delete-confirm')).toHaveTextContent('What is on my calendar tomorrow?')
    expect(deleted).toBeNull()
    await fireEvent.click(screen.getByTestId('confirm-delete'))

    await waitFor(() => expect(screen.queryByTestId('case-101')).not.toBeInTheDocument())
    expect(deleted).toBe('golden-set/101')
    expect(screen.queryByTestId('delete-confirm')).not.toBeInTheDocument()
    expect(onchanged).toHaveBeenCalled()
  })

  test('after a case is deleted, focus lands on the next row\'s Delete', async () => {
    await renderSets()

    await fireEvent.click(screen.getByTestId('delete-case-101'))
    await fireEvent.click(screen.getByTestId('confirm-delete'))
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('delete-case-102')))
  })

  test('cancelling the dialog returns focus to the button that opened it', async () => {
    await renderSets()

    await fireEvent.click(screen.getByTestId('delete-set'))
    await fireEvent.click(screen.getByText('Cancel'))
    expect(screen.queryByTestId('delete-confirm')).not.toBeInTheDocument()
    expect(document.activeElement).toBe(screen.getByTestId('delete-set'))
  })

  test('a set that runs still use stays put and the dialog says why', async () => {
    server.use(
      http.delete('/api/v1/eval/task-sets/:name', () =>
        HttpResponse.json({ error: 'task set "golden-set" has 2 run(s)' }, { status: 409 })),
    )
    const onchanged = vi.fn()
    await renderSets({ onchanged })

    await fireEvent.click(screen.getByTestId('delete-set'))
    await fireEvent.click(screen.getByTestId('confirm-delete'))

    await waitFor(() => expect(screen.getByTestId('delete-error')).toHaveTextContent('Runs still use this set'))
    expect(screen.getByTestId('delete-confirm')).toBeInTheDocument()
    expect(onchanged).not.toHaveBeenCalled()
  })

  test('deleting a set reports which one went', async () => {
    const onchanged = vi.fn()
    await renderSets({ onchanged })

    await fireEvent.click(screen.getByTestId('delete-set'))
    expect(screen.getByTestId('delete-confirm')).toHaveTextContent('Delete “golden-set” and its 3 cases?')
    await fireEvent.click(screen.getByTestId('confirm-delete'))

    await waitFor(() => expect(onchanged).toHaveBeenCalledWith('golden-set'))
  })
})

describe('EvalTestSets — export', () => {
  test('Export downloads the set as <name>.jsonl', async () => {
    // jsdom has no object URLs, so these are stubbed and put back afterwards.
    const { createObjectURL, revokeObjectURL } = URL
    const created = []
    URL.createObjectURL = vi.fn((blob) => { created.push(blob); return 'blob:x' })
    URL.revokeObjectURL = vi.fn()
    try {
      let downloaded = ''
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function () {
        downloaded = this.download
      })
      await renderSets()

      await fireEvent.click(screen.getByTestId('export-set'))

      await waitFor(() => expect(downloaded).toBe('golden-set.jsonl'))
      expect(await created[0].text()).toBe('{"prompt":"hi","category":"chat"}\n')
      expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:x')
    } finally {
      URL.createObjectURL = createObjectURL
      URL.revokeObjectURL = revokeObjectURL
    }
  })

  test('a failed export says so', async () => {
    server.use(
      http.get('/api/v1/eval/task-sets/:name/export', () =>
        HttpResponse.json({ error: 'not found' }, { status: 404 })),
    )
    await renderSets()

    await fireEvent.click(screen.getByTestId('export-set'))
    await waitFor(() => expect(screen.getByTestId('export-error')).toHaveTextContent('not found'))
  })
})
