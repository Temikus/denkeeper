import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Approvals from '../../pages/Approvals.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  vi.useFakeTimers({ shouldAdvanceTime: true })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('Approvals page', () => {
  test('renders page title', async () => {
    render(Approvals)
    expect(screen.getByText('Approvals')).toBeInTheDocument()
  })

  test('renders approval table with pending items', async () => {
    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('Run tool: web_search')).toBeInTheDocument()
    })
    // Check table headers
    expect(screen.getByText('Kind')).toBeInTheDocument()
    expect(screen.getByText('Summary')).toBeInTheDocument()
    expect(screen.getByText('Status')).toBeInTheDocument()
  })

  test('shows truncated approval ID', async () => {
    render(Approvals)
    await waitFor(() => {
      // appr-1 truncated to 8 chars
      expect(screen.getByText('appr-1…')).toBeInTheDocument()
    })
  })

  test('filter buttons switch status', async () => {
    const statuses = []
    server.use(
      http.get('/api/v1/approvals', ({ request }) => {
        const url = new URL(request.url)
        statuses.push(url.searchParams.get('status'))
        return HttpResponse.json([])
      })
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('approved')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('approved'))
    await waitFor(() => {
      expect(statuses).toContain('approved')
    })

    await fireEvent.click(screen.getByText('denied'))
    await waitFor(() => {
      expect(statuses).toContain('denied')
    })

    // "all" filter passes empty string as status, which means no ?status= param
    await fireEvent.click(screen.getByText('all'))
    await waitFor(() => {
      // The "all" filter sends status='' which results in no query param
      expect(statuses.some(s => s === null || s === '')).toBe(true)
    })
  })

  test('approve button calls API and reloads', async () => {
    let approveCalled = false
    server.use(
      http.post('/api/v1/approvals/:id/approve', () => {
        approveCalled = true
        return HttpResponse.json({ ok: true })
      })
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('Approve')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Approve'))
    await waitFor(() => {
      expect(approveCalled).toBe(true)
    })
  })

  test('deny button calls API', async () => {
    let denyCalled = false
    server.use(
      http.post('/api/v1/approvals/:id/deny', () => {
        denyCalled = true
        return HttpResponse.json({ ok: true })
      })
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('Deny')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Deny'))
    await waitFor(() => {
      expect(denyCalled).toBe(true)
    })
  })

  test('empty state message when no approvals', async () => {
    server.use(
      http.get('/api/v1/approvals', () => HttpResponse.json([]))
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText(/No approvals/)).toBeInTheDocument()
    })
  })

  test('error handling shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/approvals', () =>
        HttpResponse.json({ error: 'Server error' }, { status: 500 })
      )
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('Server error')).toBeInTheDocument()
    })
  })

  test('auto-approve rules table renders', async () => {
    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('Auto-Approve Rules')).toBeInTheDocument()
      expect(screen.getByText('web_search')).toBeInTheDocument()
    })
  })

  test('add auto-approve rule form shows on button click', async () => {
    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('+ Add Rule')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Rule'))
    await waitFor(() => {
      expect(screen.getByText('Add')).toBeInTheDocument()
      expect(screen.getByText('Cancel')).toBeInTheDocument()
      expect(screen.getByPlaceholderText('Tool name')).toBeInTheDocument()
    })
  })

  test('add rule form has agent dropdown with correct options', async () => {
    render(Approvals)
    // Wait for initial data loads (approvals + auto rules + agents)
    await waitFor(() => {
      expect(screen.getByText('Run tool: web_search')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Rule'))
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Tool name')).toBeInTheDocument()
    })

    // Agent dropdown should have the loaded agents
    await waitFor(() => {
      const agentSelect = screen.getByText('Select agent…').closest('select')
      const options = agentSelect.querySelectorAll('option')
      const optionValues = Array.from(options).map(o => o.value)
      expect(optionValues).toContain('default')
      expect(optionValues).toContain('helper')
    })

    // Add button should be disabled without agent selected
    expect(screen.getByText('Add')).toBeDisabled()
  })

  test('revoke permanent rule calls DELETE', async () => {
    let deleteCalled = false
    server.use(
      http.delete('/api/v1/auto-approve/:id', () => {
        deleteCalled = true
        return new HttpResponse(null, { status: 204 })
      })
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('Revoke')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Revoke'))
    await waitFor(() => {
      expect(deleteCalled).toBe(true)
    })
  })

  test('polling every 10s calls approvals endpoint', async () => {
    let callCount = 0
    server.use(
      http.get('/api/v1/approvals', () => {
        callCount++
        return HttpResponse.json([])
      })
    )

    render(Approvals)

    await waitFor(() => {
      // Initial load
      expect(callCount).toBeGreaterThanOrEqual(1)
    })

    const initialCount = callCount
    await vi.advanceTimersByTimeAsync(10000)

    await waitFor(() => {
      expect(callCount).toBeGreaterThan(initialCount)
    })
  })
})
