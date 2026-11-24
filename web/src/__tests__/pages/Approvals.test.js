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

  test('tool call args render as labelled rows, not one JSON line', async () => {
    const args = JSON.stringify({ code: 'var a = 1;\nreturn a;', input: { emails: [] } })
    server.use(
      http.get('/api/v1/approvals', () => HttpResponse.json([{
        id: 'appr-2', agent_name: 'default', kind: 'tool_call', status: 'pending',
        summary: `Execute tool "run_javascript" with args: ${args}`, payload: args,
        created_at: '2026-01-01T10:00:00Z', expires_at: '2026-01-01T11:00:00Z',
      }]))
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('run_javascript')).toBeInTheDocument()
    })
    expect(screen.queryByText(/Execute tool/)).not.toBeInTheDocument()
    expect(screen.getByText('code')).toBeInTheDocument()
    expect(screen.getByLabelText('run_javascript code').textContent).toBe('var a = 1;\nreturn a;')
    expect(screen.getByLabelText('run_javascript input').textContent).toBe('{\n  "emails": []\n}')
    expect(screen.queryByText('Show payload')).not.toBeInTheDocument()
  })

  test('a high-risk row shows the warning and flagged arguments above the call', async () => {
    const args = JSON.stringify({ name: 'nightly', session_tier: 'autonomous' })
    server.use(
      http.get('/api/v1/approvals', () => HttpResponse.json([{
        id: 'appr-hr', agent_name: 'default', kind: 'tool_call', status: 'pending',
        summary: `Execute tool "schedule_update" with args: ${args}`, payload: args,
        created_at: '2026-01-01T10:00:00Z', expires_at: '2026-01-01T11:00:00Z',
        risk: { level: 'high', warning: 'Sets the permission tier this schedule runs at.', flagged: [{ name: 'session_tier', value: '"autonomous"' }] },
      }]))
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('High risk')).toBeInTheDocument()
    })
    expect(screen.getByText('Sets the permission tier this schedule runs at.', { exact: false })).toBeInTheDocument()
    expect(screen.getByText('"autonomous"', { selector: '.approval-risk code' })).toBeInTheDocument()
    expect(screen.getByText('schedule_update')).toBeInTheDocument()
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

  test('config-scoped rule renders read-only (no Revoke)', async () => {
    // Config rules come from the server TOML: the API returns them with no id
    // and no created_at, and they cannot be revoked at runtime.
    server.use(
      http.get('/api/v1/auto-approve', () =>
        HttpResponse.json([
          { id: 'rule-1', agent_name: 'default', tool_name: 'web_search', scope: 'permanent', created_at: '2026-01-01T00:00:00Z', created_by: 'api' },
          { id: '', agent_name: 'default', tool_name: 'run_javascript', scope: 'config', created_by: 'config' },
        ])
      )
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('run_javascript')).toBeInTheDocument()
    })

    expect(screen.getByText('TOML-managed')).toBeInTheDocument()
    // Only the permanent rule is revocable.
    expect(screen.getAllByText('Revoke')).toHaveLength(1)
  })

  test('a rule for a high-risk tool is marked inactive', async () => {
    server.use(
      http.get('/api/v1/auto-approve', () =>
        HttpResponse.json([
          { id: 'rule-1', agent_name: 'default', tool_name: 'web_search', scope: 'permanent', created_at: '2026-01-01T00:00:00Z', created_by: 'api' },
          { id: 'rule-2', agent_name: 'default', tool_name: 'set_fallback', scope: 'permanent', created_at: '2026-01-01T00:00:00Z', created_by: 'api', inert: true },
        ])
      )
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('set_fallback')).toBeInTheDocument()
    })
    expect(screen.getAllByText('inactive (high-risk tool)')).toHaveLength(1)
    expect(screen.getByText('set_fallback').closest('tr').textContent).toContain('inactive (high-risk tool)')
  })

  test('the add-rule form shows the server refusal', async () => {
    server.use(
      http.post('/api/v1/auto-approve', () =>
        HttpResponse.json({ error: 'set_fallback is high-risk and cannot be auto-approved' }, { status: 400 })
      )
    )

    render(Approvals)
    await waitFor(() => {
      expect(screen.getByText('Run tool: web_search')).toBeInTheDocument()
    })
    await fireEvent.click(screen.getByText('+ Add Rule'))
    await waitFor(() => {
      const options = screen.getByText('Select agent…').closest('select').querySelectorAll('option')
      expect(options.length).toBeGreaterThan(1)
    })
    await fireEvent.change(screen.getByText('Select agent…').closest('select'), { target: { value: 'default' } })
    await fireEvent.input(screen.getByPlaceholderText('Tool name'), { target: { value: 'set_fallback' } })
    await fireEvent.click(screen.getByText('Add'))

    await waitFor(() => {
      expect(screen.getByText('set_fallback is high-risk and cannot be auto-approved')).toBeInTheDocument()
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
