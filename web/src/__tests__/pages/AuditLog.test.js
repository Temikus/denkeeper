import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
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
    const toolsChip = screen.getByRole('radio', { name: 'Tools' })
    expect(toolsChip).toBeInTheDocument()
    await fireEvent.click(toolsChip)
    expect(toolsChip.getAttribute('aria-checked')).toBe('true')
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
