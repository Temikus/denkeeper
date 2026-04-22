import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Sessions from '../../pages/Sessions.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Sessions page', () => {
  test('renders page title', () => {
    render(Sessions)
    expect(screen.getByText('Sessions')).toBeInTheDocument()
  })

  test('renders session list', async () => {
    render(Sessions)
    await waitFor(() => {
      // Session IDs are sliced to 14 chars
      expect(screen.getByText('sess-1')).toBeInTheDocument()
      expect(screen.getByText('sess-2')).toBeInTheDocument()
    })
  })

  test('shows empty state when no sessions', async () => {
    server.use(
      http.get('/api/v1/sessions', () => HttpResponse.json([]))
    )

    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('No sessions.')).toBeInTheDocument()
    })
  })

  test('shows select prompt before clicking a session', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('Select a session to view messages.')).toBeInTheDocument()
    })
  })

  test('clicking session loads messages', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Hello')).toBeInTheDocument()
      expect(screen.getByText('Hi there')).toBeInTheDocument()
    })
  })

  test('delete button shows confirmation modal', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    const delButtons = document.querySelectorAll('.del')
    await fireEvent.click(delButtons[0])
    await waitFor(() => {
      expect(screen.getByText('Delete Session')).toBeInTheDocument()
      expect(screen.getByText('Delete')).toBeInTheDocument()
      expect(screen.getByText('Cancel')).toBeInTheDocument()
    })
  })

  test('confirming delete removes session from list', async () => {
    let deleteCalled = false
    server.use(
      http.delete('/api/v1/sessions/:id', () => {
        deleteCalled = true
        return new HttpResponse(null, { status: 204 })
      })
    )

    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    const delButtons = document.querySelectorAll('.del')
    await fireEvent.click(delButtons[0])
    await waitFor(() => {
      expect(screen.getByText('Delete Session')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Delete'))
    await waitFor(() => {
      expect(deleteCalled).toBe(true)
    })
  })

  test('shows channel indicator for sessions with channel', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })
    // sess-1 has channel: 'work'
    expect(screen.getByText('work')).toBeInTheDocument()
  })

  test('shows channel badge in detail when session has channel', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })
    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Channel: work')).toBeInTheDocument()
    })
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/sessions', () =>
        HttpResponse.json({ error: 'Database error' }, { status: 500 })
      )
    )

    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('Database error')).toBeInTheDocument()
    })
  })
})
