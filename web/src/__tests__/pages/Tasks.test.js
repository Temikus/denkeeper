import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { taskDetail } from '../../test/handlers.js'
import { token, authMode } from '../../store.js'
import { currentQuery } from '../../router.js'
import { taskUpdates } from '../../wsStore.js'
import Tasks from '../../pages/Tasks.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  currentQuery.set(new URLSearchParams())
  vi.useFakeTimers({ shouldAdvanceTime: true })
})

afterEach(() => {
  vi.useRealTimers()
})

const column = name => screen.getByRole('region', { name: new RegExp(name, 'i') })

describe('Tasks board', () => {
  test('puts each task in its column, blocked with In progress', async () => {
    render(Tasks)
    await screen.findByText('Add Fastmail to the weekly digest')

    expect(within(column('To do')).getByText('Renew the denkeeper.dev domain')).toBeInTheDocument()
    expect(within(column('To do')).getByText('Unassigned')).toBeInTheDocument()
    expect(within(column('To do')).getByText('anyone may claim')).toBeInTheDocument()
    expect(within(column('In progress')).getByText("Plan next week's groceries")).toBeInTheDocument()
    expect(within(column('In progress')).getByText('blocked')).toHaveAttribute('title', 'No shop picked')
    expect(within(column('Review')).getByText('your review')).toBeInTheDocument()
    expect(within(column('Done')).getByText('Book the boiler service')).toBeInTheDocument()
  })

  test('a subtask names its parent and a parent shows progress', async () => {
    render(Tasks)
    await screen.findByText('Check Fastmail API rate limits')
    expect(screen.getByText(/DK-134 · ↳ DK-131/)).toBeInTheDocument()
    expect(screen.getByText('subtasks 1/2')).toBeInTheDocument()
  })

  test('Waiting on you counts and filters blocked, review and your tasks', async () => {
    render(Tasks)
    const chip = await screen.findByRole('radio', { name: 'Waiting on you · 3' })
    await fireEvent.click(chip)
    expect(screen.queryByText('Renew the denkeeper.dev domain')).toBeNull()
    expect(screen.getByText('Pick a Saturday for the passport appointment')).toBeInTheDocument()
    expect(screen.getByText('Summarise October spending by category')).toBeInTheDocument()
    expect(screen.getByText("Plan next week's groceries")).toBeInTheDocument()
  })

  test('cards link to the task', async () => {
    render(Tasks)
    const title = await screen.findByText('Renew the denkeeper.dev domain')
    expect(title.closest('a')).toHaveAttribute('href', '#/tasks?id=130')
  })

  test('shows how to turn tasks on when the server has them off', async () => {
    server.use(http.get('/api/v1/tasks', () => HttpResponse.json({ error: 'tasks are off' }, { status: 503 })))
    render(Tasks)
    expect(await screen.findByText('Tasks are off')).toBeInTheDocument()
    expect(screen.getByText(/enabled = true/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '+ New task' })).toBeNull()
  })

  test('empty board invites the first task', async () => {
    server.use(http.get('/api/v1/tasks', () => HttpResponse.json({ tasks: [] })))
    render(Tasks)
    expect(await screen.findByText(/No tasks yet/)).toBeInTheDocument()
  })

  test('a failed load shows the error, not an empty board', async () => {
    server.use(http.get('/api/v1/tasks', () => HttpResponse.json({ error: 'database is locked' }, { status: 500 })))
    render(Tasks)
    expect(await screen.findByText('database is locked')).toBeInTheDocument()
    expect(screen.queryByText(/No tasks yet/)).toBeNull()
  })

  test('re-reads the board on a task_updated frame', async () => {
    let calls = 0
    server.use(http.get('/api/v1/tasks', () => { calls++; return HttpResponse.json({ tasks: [] }) }))
    render(Tasks)
    await waitFor(() => expect(calls).toBe(1))
    taskUpdates.update(t => ({ seq: t.seq + 1, taskID: 130 }))
    taskUpdates.update(t => ({ seq: t.seq + 1, taskID: 131 }))
    await vi.advanceTimersByTimeAsync(300)
    await waitFor(() => expect(calls).toBe(2))
  })
})

describe('Task composer', () => {
  test('files an unassigned task with review on by default', async () => {
    let body
    server.use(http.post('/api/v1/tasks', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 140, key: 'DK-140' }, { status: 201 })
    }))
    render(Tasks)
    await fireEvent.click(await screen.findByRole('button', { name: '+ New task' }))

    expect(screen.getByTestId('task-assignee')).toHaveValue('')
    expect(screen.getByText('Waits in To do. Anyone may claim it.')).toBeInTheDocument()

    const title = screen.getByLabelText('Title')
    await fireEvent.input(title, { target: { value: 'Water the plants' } })
    await fireEvent.keyDown(title, { key: 'Enter' })

    await waitFor(() => expect(body).toEqual({ title: 'Water the plants', body: '', assignee: '', no_review: false }))
    await waitFor(() => expect(screen.queryByRole('form', { name: 'New task' })).toBeNull())
    await waitFor(() => expect(screen.getByRole('button', { name: '+ New task' })).toHaveFocus())
  })

  test('filing switches back to All so the new task is in view', async () => {
    server.use(http.post('/api/v1/tasks', () => HttpResponse.json({ id: 142 }, { status: 201 })))
    render(Tasks)
    await fireEvent.click(await screen.findByRole('radio', { name: /Waiting on you/ }))
    await fireEvent.click(screen.getByRole('button', { name: '+ New task' }))
    await fireEvent.input(screen.getByLabelText('Title'), { target: { value: 'New one' } })
    await fireEvent.click(screen.getByRole('button', { name: 'File task' }))
    await waitFor(() => expect(screen.getByRole('radio', { name: 'All' })).toHaveAttribute('aria-checked', 'true'))
  })

  test('assigning to an agent says when it starts and sends the member ID', async () => {
    let body
    server.use(http.post('/api/v1/tasks', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 141 }, { status: 201 })
    }))
    render(Tasks)
    await fireEvent.click(await screen.findByRole('button', { name: '+ New task' }))
    await waitFor(() => expect(screen.getByRole('option', { name: 'pamela' })).toBeInTheDocument())

    await fireEvent.change(screen.getByTestId('task-assignee'), { target: { value: 'agent:pamela' } })
    expect(screen.getByText('pamela picks it up when it next checks its tasks.')).toBeInTheDocument()
    await fireEvent.click(screen.getByLabelText("I review before it's done"))
    await fireEvent.input(screen.getByLabelText('Title'), { target: { value: 'Draft the digest' } })
    await fireEvent.click(screen.getByRole('button', { name: 'File task' }))

    await waitFor(() => expect(body).toMatchObject({ assignee: 'agent:pamela', no_review: true }))
  })

  test('Escape cancels and a refusal stays in the form', async () => {
    server.use(http.post('/api/v1/tasks', () => HttpResponse.json({ error: 'title is too long' }, { status: 400 })))
    render(Tasks)
    await fireEvent.click(await screen.findByRole('button', { name: '+ New task' }))
    await fireEvent.input(screen.getByLabelText('Title'), { target: { value: 'x' } })
    await fireEvent.click(screen.getByRole('button', { name: 'File task' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('title is too long')

    await fireEvent.keyDown(screen.getByLabelText('Title'), { key: 'Escape' })
    expect(screen.queryByLabelText('Title')).toBeNull()
  })
})

describe('Task detail', () => {
  beforeEach(() => currentQuery.set(new URLSearchParams('id=131')))

  test('reads the thread top to bottom: request, events, comments', async () => {
    render(Tasks)
    expect(await screen.findByRole('heading', { name: 'Add Fastmail to the weekly digest' })).toBeInTheDocument()
    expect(screen.getByText('You filed this')).toBeInTheDocument()
    expect(screen.getByText('The Sunday digest only covers Gmail.')).toBeInTheDocument()
    expect(screen.getByText('pamela claimed it')).toBeInTheDocument()
    expect(screen.getByText('Plan: Scout compares JMAP and IMAP.')).toBeInTheDocument()
    expect(screen.getByText('den agent')).toBeInTheDocument()
    expect(screen.getByText('pamela filed DK-134: Check Fastmail API rate limits')).toBeInTheDocument()
    expect(screen.getByText('Subtasks · 1 of 2 done')).toBeInTheDocument()
    expect(screen.getByText('Held by').closest('.field').querySelector('dd')).toHaveTextContent(/^pamela · \d+[smhd] Release$/)
  })

  test('status change saves in place; blocked asks for a reason first', async () => {
    const patches = []
    server.use(http.patch('/api/v1/tasks/:id', async ({ request }) => {
      patches.push(await request.json())
      return HttpResponse.json(taskDetail.task)
    }))
    render(Tasks)
    const status = await screen.findByLabelText('Status')

    await fireEvent.change(status, { target: { value: 'review' } })
    await waitFor(() => expect(patches).toEqual([{ status: 'review' }]))

    await fireEvent.change(status, { target: { value: 'blocked' } })
    expect(patches).toHaveLength(1)
    expect(screen.getByLabelText("What's in the way?")).toHaveFocus()
    await fireEvent.input(screen.getByLabelText("What's in the way?"), { target: { value: 'Need an app password' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Mark blocked' }))
    await waitFor(() => expect(patches[1]).toEqual({ status: 'blocked', reason: 'Need an app password' }))
  })

  test('a refused change shows why and puts the select back', async () => {
    server.use(http.patch('/api/v1/tasks/:id', () => HttpResponse.json({ error: 'DK-131 needs review by human.' }, { status: 400 })))
    render(Tasks)
    const assignee = await screen.findByLabelText('Assignee')
    await fireEvent.change(assignee, { target: { value: 'agent:scout' } })
    expect(await screen.findByRole('alert')).toHaveTextContent('needs review by human')
    expect(assignee).toHaveValue('agent:pamela')
  })

  test('release asks inline before clearing the claim', async () => {
    let released = false
    server.use(http.post('/api/v1/tasks/:id/release', () => { released = true; return HttpResponse.json(taskDetail.task) }))
    render(Tasks)
    await fireEvent.click(await screen.findByRole('button', { name: 'Release' }))
    const confirm = screen.getByRole('group', { name: 'Confirm release' })
    expect(confirm).toHaveTextContent("Release pamela's claim? The task goes back to To do.")
    expect(released).toBe(false)
    await fireEvent.click(within(confirm).getByRole('button', { name: 'Release' }))
    await waitFor(() => expect(released).toBe(true))
  })

  test('posts a comment', async () => {
    let body
    server.use(http.post('/api/v1/tasks/:id/comments', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ id: 9 }, { status: 201 })
    }))
    render(Tasks)
    const box = await screen.findByLabelText('Comment')
    expect(screen.getByText('pamela reads it when it next checks this task.')).toBeInTheDocument()
    await fireEvent.input(box, { target: { value: 'Use the read-only token.' } })
    await fireEvent.click(screen.getByRole('button', { name: 'Comment' }))
    await waitFor(() => expect(body).toEqual({ body: 'Use the read-only token.' }))
  })

  test('Keep closes the release confirm and returns focus to Release', async () => {
    render(Tasks)
    const link = await screen.findByRole('button', { name: 'Release' })
    await fireEvent.click(link)
    const confirm = screen.getByRole('group', { name: 'Confirm release' })
    expect(within(confirm).getByRole('button', { name: 'Release' })).toHaveFocus()
    await fireEvent.click(within(confirm).getByRole('button', { name: 'Keep' }))
    await waitFor(() => expect(link).toHaveFocus())
  })

  test('a failed load shows the error with a way to retry', async () => {
    let fail = true
    server.use(http.get('/api/v1/tasks/:id', () => fail
      ? HttpResponse.json({ error: 'database is locked' }, { status: 500 })
      : HttpResponse.json(taskDetail)))
    render(Tasks)
    expect(await screen.findByText('database is locked')).toBeInTheDocument()
    fail = false
    await fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByRole('heading', { name: 'Add Fastmail to the weekly digest' })).toBeInTheDocument()
  })

  test('a missing task says so', async () => {
    server.use(http.get('/api/v1/tasks/:id', () => HttpResponse.json({ error: 'not found' }, { status: 404 })))
    currentQuery.set(new URLSearchParams('id=999'))
    render(Tasks)
    expect(await screen.findByText(/No task 999/)).toBeInTheDocument()
  })
})
