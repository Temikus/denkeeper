import { describe, test, expect, beforeEach } from 'vitest'
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

  // The search box advertises `tool:`/`agent:` tokens, so they have to reach
  // the API as the filters they name — `agent:` in particular is an exact
  // match server-side and would never hit as a summary substring.
  describe('token search syntax', () => {
    // Returns the query params of the most recent /api/v1/audit request.
    function captureAuditQuery() {
      const seen = []
      server.use(
        http.get('/api/v1/audit', ({ request }) => {
          seen.push(new URL(request.url).searchParams)
          return HttpResponse.json({ events: auditEvents, total: auditEvents.length })
        }),
      )
      return seen
    }

    async function typeSearch(text) {
      const input = screen.getByPlaceholderText('Search events')
      await fireEvent.input(input, { target: { value: text } })
    }

    test('agent: is sent as the exact-match agent filter, not as search text', async () => {
      const seen = captureAuditQuery()
      render(AuditLog)
      await waitFor(() => expect(seen.length).toBe(1))

      await typeSearch('agent:planner')
      await waitFor(() => expect(seen.length).toBe(2))
      expect(seen[1].get('agent')).toBe('planner')
      expect(seen[1].get('search')).toBeNull()
    })

    test('tool: is sent as the search term', async () => {
      const seen = captureAuditQuery()
      render(AuditLog)
      await waitFor(() => expect(seen.length).toBe(1))

      await typeSearch('tool:web_search')
      await waitFor(() => expect(seen.length).toBe(2))
      expect(seen[1].get('search')).toBe('web_search')
      expect(seen[1].get('agent')).toBeNull()
    })

    test('tokens combine with free text in one request', async () => {
      const seen = captureAuditQuery()
      render(AuditLog)
      await waitFor(() => expect(seen.length).toBe(1))

      await typeSearch('agent:planner tool:web_search timeout')
      await waitFor(() => expect(seen.length).toBe(2))
      expect(seen[1].get('agent')).toBe('planner')
      expect(seen[1].get('search')).toBe('web_search timeout')
    })

    test('untokenized text still searches summaries', async () => {
      const seen = captureAuditQuery()
      render(AuditLog)
      await waitFor(() => expect(seen.length).toBe(1))

      await typeSearch('database error')
      await waitFor(() => expect(seen.length).toBe(2))
      expect(seen[1].get('search')).toBe('database error')
      expect(seen[1].get('agent')).toBeNull()
    })

    // The chips describe the request, not the tokens: two tool: tokens are one
    // phrase match on the wire and must not read as two independent filters.
    test('active chips describe the query actually sent', async () => {
      render(AuditLog)
      await waitFor(() => {
        expect(screen.getByText('agent:planner')).toBeInTheDocument()
      })
      expect(screen.getByText('try')).toBeInTheDocument()

      await typeSearch('agent:default tool:kv_get tool:kv_list')

      await waitFor(() => {
        expect(screen.getByText('filtering')).toBeInTheDocument()
      })
      expect(screen.getByText('agent = default')).toBeInTheDocument()
      expect(screen.getByText('summary contains "kv_get kv_list"')).toBeInTheDocument()
      // The example chips are gone, so nothing claims a filter that isn't set.
      expect(screen.queryByText('try')).not.toBeInTheDocument()
      expect(screen.queryByText('tool:name')).not.toBeInTheDocument()
    })

    test('untokenized text is chipped too, so the summary match is visible', async () => {
      render(AuditLog)
      await waitFor(() => expect(screen.getByText('try')).toBeInTheDocument())

      await typeSearch('database error')

      await waitFor(() => {
        expect(screen.getByText('summary contains "database error"')).toBeInTheDocument()
      })
      expect(screen.queryByText(/^agent = /)).not.toBeInTheDocument()
    })

    // An exact-match agent filter that misses looks identical to an empty log
    // unless the empty state says which filter produced it.
    test('empty state names the filters that produced it', async () => {
      server.use(
        http.get('/api/v1/audit', () => HttpResponse.json({ events: [], total: 0 })),
      )
      render(AuditLog)
      await waitFor(() => {
        expect(screen.getByText('No audit events found.')).toBeInTheDocument()
      })

      await typeSearch('agent:typo tool:kv_get')
      await waitFor(() => {
        expect(screen.getByText('No audit events found for agent "typo" matching "kv_get".')).toBeInTheDocument()
      })
    })
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
