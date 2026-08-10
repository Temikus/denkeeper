import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import { auditEvents, auditStats } from '../../test/fixtures/index.js'

// Import lazily after mocks are in place
const AuditLog = (await import('../../pages/AuditLog.svelte')).default

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('AuditLog page', () => {
  test('shows loading state initially', () => {
    server.use(
      http.get('/api/v1/audit', () => new Promise(() => {})),
      http.get('/api/v1/audit/stats', () => new Promise(() => {})),
    )
    render(AuditLog)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  test('renders page header and filter controls after load', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    // Filter chips — "All" appears in both category and status filter groups
    expect(screen.getAllByText('All').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('Tools')).toBeInTheDocument()
    expect(screen.getByText('LLM')).toBeInTheDocument()
    expect(screen.getByText('Approvals')).toBeInTheDocument()
    // Time range chips
    expect(screen.getByText('24h')).toBeInTheDocument()
    expect(screen.getByText('7d')).toBeInTheDocument()
  })

  test('renders stats card with counts from API', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('5')).toBeInTheDocument() // total events
    })
    expect(screen.getByText('events')).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()  // tool_call count
    // llm count (1) and error count (1) both appear — use getAllByText
    expect(screen.getAllByText('1').length).toBeGreaterThanOrEqual(2)
  })

  test('renders timeline view with session groups and standalone events', async () => {
    render(AuditLog)
    await waitFor(() => {
      // Session events (conversation_id: 'chan:general') should be grouped
      // Standalone event (evt-3 approval, no conversation_id) should appear separately
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    // Timeline is default view
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  test('switches to table view on click', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    await fireEvent.click(screen.getByText('Table'))
    await waitFor(() => {
      expect(screen.getByRole('table')).toBeInTheDocument()
    })
    // Table headers — "Type" also appears as filter label, use getAllByText
    expect(screen.getByText('Time')).toBeInTheDocument()
    expect(screen.getAllByText('Type').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('Summary')).toBeInTheDocument()
    // "Status" also appears in filter chips — use getAllByText
    expect(screen.getAllByText('Status').length).toBeGreaterThanOrEqual(1)
  })

  test('table view renders event rows', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    await fireEvent.click(screen.getByText('Table'))
    await waitFor(() => {
      expect(screen.getByRole('table')).toBeInTheDocument()
    })
    // Should show event summaries in rows
    expect(screen.getByText('search("denkeeper")')).toBeInTheDocument()
    expect(screen.getByText('claude-3-opus')).toBeInTheDocument()
    expect(screen.getByText('web_search approved')).toBeInTheDocument()
  })

  test('table view shows FAILED pill for error events', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    await fireEvent.click(screen.getByText('Table'))
    await waitFor(() => {
      expect(screen.getByRole('table')).toBeInTheDocument()
    })
    expect(screen.getByText('FAILED')).toBeInTheDocument()
  })

  test('switches between time ranges', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    await fireEvent.click(screen.getByText('1h'))
    expect(screen.getByText('last 1h')).toBeInTheDocument()

    await fireEvent.click(screen.getByText('7d'))
    expect(screen.getByText('last 7d')).toBeInTheDocument()
  })

  test('shows empty state when no events', async () => {
    server.use(
      http.get('/api/v1/audit', () => HttpResponse.json({ events: [], total: 0 })),
    )
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('No audit events found.')).toBeInTheDocument()
    })
  })

  test('shows error banner on load failure', async () => {
    server.use(
      http.get('/api/v1/audit', () => HttpResponse.json({ error: 'Database error' }, { status: 500 })),
    )
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText(/Database error/i)).toBeInTheDocument()
    })
  })

  test('shows Follow button that toggles active state', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    const followBtn = screen.getByText('Follow')
    expect(followBtn).toBeInTheDocument()
    await fireEvent.click(followBtn)
    // After click, button should be active (class changes)
    expect(followBtn.classList.contains('active')).toBe(true)
    // Click again to deactivate
    await fireEvent.click(followBtn)
    expect(followBtn.classList.contains('active')).toBe(false)
  })

  test('shows search input', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    expect(screen.getByPlaceholderText('Search events')).toBeInTheDocument()
  })

  test('category filter chips are clickable', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    const toolsChip = screen.getByRole('button', { name: 'Tools' })
    expect(toolsChip).toBeInTheDocument()
    await fireEvent.click(toolsChip)
    expect(toolsChip.getAttribute('aria-pressed')).toBe('true')
  })

  test('Type and Status chips select several values at once', async () => {
    let lastUrl = ''
    server.use(
      http.get('/api/v1/audit', ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json({ events: [], total: 0 })
      }),
    )
    render(AuditLog)
    await waitFor(() => expect(lastUrl).not.toBe(''))

    await fireEvent.click(screen.getByRole('button', { name: 'Tools' }))
    await fireEvent.click(screen.getByRole('button', { name: 'LLM' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Approvals' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Error' }))

    await waitFor(() => {
      const params = new URL(lastUrl).searchParams
      expect(params.get('category')).toBe('tool_call,llm,approval')
      expect(params.get('status')).toBe('error')
    })
    expect(screen.getByRole('button', { name: 'LLM' })).toHaveAttribute('aria-pressed', 'true')
  })

  test('deselecting a Type chip narrows the union back down', async () => {
    let lastUrl = ''
    server.use(
      http.get('/api/v1/audit', ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json({ events: [], total: 0 })
      }),
    )
    render(AuditLog)
    await waitFor(() => expect(lastUrl).not.toBe(''))

    await fireEvent.click(screen.getByRole('button', { name: 'Tools' }))
    await fireEvent.click(screen.getByRole('button', { name: 'LLM' }))
    await waitFor(() => {
      expect(new URL(lastUrl).searchParams.get('category')).toBe('tool_call,llm')
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Tools' }))
    await waitFor(() => {
      expect(new URL(lastUrl).searchParams.get('category')).toBe('llm')
    })
  })

  test('the All chip clears a multi-selection and sends no filter', async () => {
    let lastUrl = ''
    server.use(
      http.get('/api/v1/audit', ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json({ events: [], total: 0 })
      }),
    )
    render(AuditLog)
    await waitFor(() => expect(lastUrl).not.toBe(''))

    const typeBar = screen.getByRole('toolbar', { name: 'Category filter' })
    await fireEvent.click(screen.getByRole('button', { name: 'Tools' }))
    await fireEvent.click(screen.getByRole('button', { name: 'LLM' }))
    await waitFor(() => {
      expect(new URL(lastUrl).searchParams.get('category')).toBe('tool_call,llm')
    })

    // "All" is the clear-all chip; an empty selection must drop the param
    // rather than send `category=`, which would match nothing server-side.
    await fireEvent.click(within(typeBar).getByRole('button', { name: 'All' }))
    await waitFor(() => {
      expect(new URL(lastUrl).searchParams.has('category')).toBe(false)
    })
    expect(within(typeBar).getByRole('button', { name: 'All' })).toHaveAttribute('aria-pressed', 'true')
  })

  test('the range bar is a single tab stop with arrow-key selection', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })

    const group = screen.getByRole('radiogroup', { name: 'Time range' })
    const chips = [...group.querySelectorAll('[role="radio"]')]
    // "24h" is selected on load, so it owns the group's single tab stop.
    expect(chips[1]).toHaveAttribute('tabindex', '0')
    expect(chips.filter((_, i) => i !== 1).every(c => c.getAttribute('tabindex') === '-1')).toBe(true)

    await fireEvent.keyDown(chips[1], { key: 'ArrowRight' })
    await waitFor(() => {
      expect(chips[2]).toHaveAttribute('aria-checked', 'true')
    })
    expect(chips[2]).toHaveAttribute('tabindex', '0')
    expect(chips[1]).toHaveAttribute('tabindex', '-1')

    await fireEvent.keyDown(chips[2], { key: 'Home' })
    await waitFor(() => {
      expect(chips[0]).toHaveAttribute('aria-checked', 'true')
    })
  })

  test('arrows move focus across Type chips without selecting', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })

    const group = screen.getByRole('toolbar', { name: 'Category filter' })
    const chips = [...group.querySelectorAll('.chip')]
    chips[0].focus()
    await fireEvent.keyDown(chips[0], { key: 'ArrowRight' })
    expect(document.activeElement).toBe(chips[1])
    expect(chips[1]).toHaveAttribute('aria-pressed', 'false')
  })

  test('rapid chip toggling coalesces into a single refetch', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/audit', () => {
        calls++
        return HttpResponse.json({ events: [], total: 0 })
      }),
    )
    render(AuditLog)
    await waitFor(() => expect(calls).toBe(1))

    const group = screen.getByRole('toolbar', { name: 'Category filter' })
    const chips = [...group.querySelectorAll('.chip')]
    await fireEvent.click(chips[1])
    await fireEvent.click(chips[2])
    await fireEvent.click(chips[3])

    // Three filter changes in quick succession; the page must not fire one
    // request per click.
    expect(chips[3]).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(calls).toBeGreaterThan(1))
    expect(calls).toBeLessThanOrEqual(2)
  })

  test('the status bar is a labelled toolbar and the range bar a radiogroup', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    expect(screen.getByRole('toolbar', { name: 'Status filter' })).toBeInTheDocument()
    expect(screen.getByRole('radiogroup', { name: 'Time range' })).toBeInTheDocument()
  })

  test('shows load more button when events < total', async () => {
    server.use(
      http.get('/api/v1/audit', () => HttpResponse.json({ events: auditEvents, total: 100 })),
    )
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Load older events')).toBeInTheDocument()
    })
  })

  test('no load more button when events == total', async () => {
    render(AuditLog)
    await waitFor(() => {
      expect(screen.getByText('Audit log')).toBeInTheDocument()
    })
    expect(screen.queryByText('Load older events')).not.toBeInTheDocument()
  })
})

describe('AuditLog in-flight state', () => {
  // `.results` is the only element carrying aria-busy, and role="status" is
  // unique to the search-card marker, so both are safe page-wide handles.
  const results = (container) => container.querySelector('.results')
  const settled = (container) =>
    waitFor(() => expect(results(container)).toHaveAttribute('aria-busy', 'false'))

  test('marks the search card in-flight while a filter refetch is pending', async () => {
    const { container } = render(AuditLog)
    await settled(container)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()

    await fireEvent.click(screen.getByRole('button', { name: 'Tools' }))
    expect(screen.getByRole('status')).toHaveTextContent('Searching')

    await waitFor(() => {
      expect(screen.queryByRole('status')).not.toBeInTheDocument()
    })
  })

  test('dims the current results while a refetch is pending, then restores', async () => {
    const { container } = render(AuditLog)
    await settled(container)
    expect(results(container)).not.toHaveClass('is-refreshing')

    await fireEvent.click(screen.getByRole('button', { name: 'Error' }))
    expect(results(container)).toHaveAttribute('aria-busy', 'true')
    expect(results(container)).toHaveClass('is-refreshing')

    await settled(container)
    expect(results(container)).not.toHaveClass('is-refreshing')
  })

  test('marks in-flight from the keystroke, before the debounce fires a request', async () => {
    let calls = 0
    server.use(
      http.get('/api/v1/audit', () => {
        calls++
        return HttpResponse.json({ events: auditEvents, total: auditEvents.length })
      }),
    )
    const { container } = render(AuditLog)
    await settled(container)
    const before = calls

    await fireEvent.input(screen.getByPlaceholderText('Search events'), { target: { value: 'tool:web' } })

    // The 300ms debounce has not elapsed, so nothing has gone out yet — but the
    // user is already waiting, so the page must already read as busy.
    expect(calls).toBe(before)
    expect(screen.getByRole('status')).toHaveTextContent('Searching')
    expect(results(container)).toHaveAttribute('aria-busy', 'true')

    await waitFor(() => expect(calls).toBe(before + 1))
    await settled(container)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  test('a search that will match nothing reads as in-flight before it reads as empty', async () => {
    server.use(
      http.get('/api/v1/audit', ({ request }) => {
        const search = new URL(request.url).searchParams.get('search') || ''
        // `agent:`-style queries are matched against summary text, so a name
        // that appears in no summary is a perfectly valid query that simply
        // returns zero rows — the case that used to be indistinguishable from
        // a load still in flight.
        if (search.startsWith('agent:')) return HttpResponse.json({ events: [], total: 0 })
        return HttpResponse.json({ events: auditEvents, total: auditEvents.length })
      }),
    )
    const { container } = render(AuditLog)
    await settled(container)
    expect(screen.queryByText('No audit events found.')).not.toBeInTheDocument()

    await fireEvent.input(screen.getByPlaceholderText('Search events'), { target: { value: 'agent:plannr' } })
    // This window used to be indistinguishable from a settled empty result.
    expect(screen.getByRole('status')).toHaveTextContent('Searching')

    await waitFor(() => {
      expect(screen.getByText('No audit events found.')).toBeInTheDocument()
    })
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(results(container)).not.toHaveClass('is-refreshing')
  })

  test('the in-flight marker is absent while a response is still outstanding on Follow polls', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      let calls = 0
      let releasePoll
      server.use(
        http.get('/api/v1/audit', async () => {
          calls++
          // Hold every poll open so the assertions below run mid-request.
          if (calls > 1) await new Promise((r) => { releasePoll = r })
          return HttpResponse.json({ events: auditEvents, total: auditEvents.length })
        }),
      )
      const { container } = render(AuditLog)
      await settled(container)

      await fireEvent.click(screen.getByText('Follow'))
      await vi.advanceTimersByTimeAsync(5000)

      // Follow refreshes in the background: the user did not ask for it, so it
      // must not dim their list or claim a search is running.
      expect(calls).toBe(2)
      expect(results(container)).toHaveAttribute('aria-busy', 'false')
      expect(screen.queryByRole('status')).not.toBeInTheDocument()

      releasePoll?.()
      await fireEvent.click(screen.getByText('Follow'))
    } finally {
      vi.useRealTimers()
    }
  })
})
