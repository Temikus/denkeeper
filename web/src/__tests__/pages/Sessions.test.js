import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte'
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
      expect(screen.getByText('No sessions. Scheduled runs are hidden.')).toBeInTheDocument()
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

  // --- Telemetry stat cards ---

  test('shows cost stat card after selecting session', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('$0.0284')).toBeInTheDocument()
    })
  })

  test('shows token stat cards', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('4.2k')).toBeInTheDocument()
      expect(screen.getByText('1.8k')).toBeInTheDocument()
    })
  })

  test('shows cached tokens when present', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Cached')).toBeInTheDocument()
      expect(screen.getByText('500')).toBeInTheDocument()
    })
  })

  test('hides cached tokens when zero', async () => {
    server.use(
      http.get('/api/v1/sessions/:id/stats', () => HttpResponse.json({
        conversation_id: 'sess-1',
        total_messages: 12,
        total_cost: 0.01,
        total_tokens_prompt: 1000,
        total_tokens_completion: 500,
        total_tokens_cached: 0,
        total_tool_calls: 0,
        total_tool_errors: 0,
        last_model: 'claude-3-opus',
        last_provider: 'anthropic',
      }))
    )

    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Cost')).toBeInTheDocument()
    })
    expect(screen.queryByText('Cached')).not.toBeInTheDocument()
  })

  test('shows tool calls count in stat card', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Tool Calls')).toBeInTheDocument()
    })
    // stat card value shows "3" (total_tool_calls from fixture)
    const toolCallsCard = screen.getByText('Tool Calls').closest('.stat-card')
    expect(toolCallsCard.querySelector('.stat-value').textContent).toBe('3')
  })

  test('shows model and provider stat cards', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Model')).toBeInTheDocument()
      expect(screen.getByText('Provider')).toBeInTheDocument()
    })
    const modelCard = screen.getByText('Model').closest('.stat-card')
    expect(modelCard.querySelector('.stat-value').textContent).toBe('claude-3-opus')
    const providerCard = screen.getByText('Provider').closest('.stat-card')
    expect(providerCard.querySelector('.stat-value').textContent).toBe('anthropic')
  })

  // --- Tool calls table ---

  test('shows tool calls expandable section', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Tool Calls (2)')).toBeInTheDocument()
    })
  })

  test('tool calls table shows details', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Tool Calls (2)')).toBeInTheDocument()
    })
    expect(screen.getByText('web_search')).toBeInTheDocument()
    expect(screen.getByText('search-mcp')).toBeInTheDocument()
    expect(screen.getByText('1.2s')).toBeInTheDocument()
    expect(screen.getByText('read_file')).toBeInTheDocument()
    expect(screen.getByText('fs-mcp')).toBeInTheDocument()
    expect(screen.getByText('45ms')).toBeInTheDocument()
  })

  // --- Skills usage table ---

  test('shows skills section', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Skills (1)')).toBeInTheDocument()
    })
    expect(screen.getByText('greeting')).toBeInTheDocument()
    expect(screen.getByText('trigger')).toBeInTheDocument()
  })

  // --- Pagination ---

  test('shows Load more when more sessions exist', async () => {
    server.use(
      http.get('/api/v1/sessions', () => HttpResponse.json({
        sessions: [
          { id: 'sess-1', agent: 'default', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T12:00:00Z', message_count: 12, channel: 'work' },
          { id: 'sess-2', agent: 'helper', created_at: '2026-01-02T00:00:00Z', message_count: 5, channel: '' },
        ],
        total: 100, limit: 50, offset: 0,
      }))
    )

    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })
    expect(screen.getByText('Load more')).toBeInTheDocument()
  })

  test('hides Load more when all loaded', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })
    // Default fixture: total: 2, sessions.length: 2
    expect(screen.queryByText('Load more')).not.toBeInTheDocument()
  })

  // --- Session actions ---

  test('session actions render for selected session', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Clear')).toBeInTheDocument()
      expect(screen.getByText('Compact')).toBeInTheDocument()
    })
  })

  test('Clear button shows confirmation modal', async () => {
    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Clear')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Clear'))
    await waitFor(() => {
      expect(screen.getByText('Clear Session')).toBeInTheDocument()
      expect(screen.getByText(/Remove all messages from session/)).toBeInTheDocument()
    })
  })

  test('confirming clear calls API and removes messages', async () => {
    let clearCalled = false
    server.use(
      http.post('/api/v1/sessions/:id/clear', () => {
        clearCalled = true
        return new HttpResponse(null, { status: 204 })
      })
    )

    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Hello')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Clear'))
    await waitFor(() => {
      expect(screen.getByText('Clear Session')).toBeInTheDocument()
    })

    // Click the Clear button in the modal
    const modal = screen.getByRole('dialog')
    await fireEvent.click(within(modal).getByRole('button', { name: 'Clear' }))

    await waitFor(() => {
      expect(clearCalled).toBe(true)
      expect(screen.queryByText('Hello')).not.toBeInTheDocument()
    })
  })

  test('Compact button calls API', async () => {
    let compactCalled = false
    server.use(
      http.post('/api/v1/sessions/:id/compact', () => {
        compactCalled = true
        return HttpResponse.json({ summary: 'Session compacted.' })
      })
    )

    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Compact')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Compact'))
    await waitFor(() => {
      expect(compactCalled).toBe(true)
    })
  })

  // --- Partial failure ---

  test('messages load even when stats fail', async () => {
    server.use(
      http.get('/api/v1/sessions/:id/stats', () =>
        HttpResponse.json({ error: 'stats unavailable' }, { status: 500 })
      )
    )

    render(Sessions)
    await waitFor(() => {
      expect(screen.getByText('sess-1')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('sess-1').closest('.item'))
    await waitFor(() => {
      expect(screen.getByText('Hello')).toBeInTheDocument()
      expect(screen.getByText('Hi there')).toBeInTheDocument()
    })
    // Stats grid should not be present
    expect(document.querySelector('.stats-grid')).not.toBeInTheDocument()
  })
})

describe('Sessions page: scheduled runs toggle', () => {
  function serveByFlag() {
    const seen = []
    server.use(
      http.get('/api/v1/sessions', ({ request }) => {
        const include = new URL(request.url).searchParams.get('include_scheduled') === 'true'
        seen.push(include)
        const sessions = [{ id: 'chan:main', created_at: '2026-01-01T00:00:00Z', message_count: 2 }]
        if (include) sessions.push({ id: 'sched:daily:1', created_at: '2026-01-02T00:00:00Z', message_count: 2 })
        return HttpResponse.json({ sessions, total: sessions.length, limit: 50, offset: 0 })
      })
    )
    return seen
  }

  test('hides scheduled runs by default and shows them when toggled on', async () => {
    const seen = serveByFlag()
    render(Sessions)
    await waitFor(() => expect(screen.getByText('chan:main')).toBeInTheDocument())
    expect(screen.queryByText('scheduled')).not.toBeInTheDocument()

    await fireEvent.click(screen.getByLabelText('Show scheduled runs'))
    // Rendered as a "scheduled" pill plus the schedule name, not the raw ID.
    await waitFor(() => expect(screen.getByText('scheduled')).toBeInTheDocument())
    expect(screen.getByText('scheduled').closest('.sid')).toHaveTextContent('scheduled daily')
    expect(seen).toEqual([false, true])
  })

  test('drops a Load more page that resolves after the filter changed', async () => {
    let releaseStale
    server.use(
      http.get('/api/v1/sessions', async ({ request }) => {
        const params = new URL(request.url).searchParams
        const include = params.get('include_scheduled') === 'true'
        if (params.get('offset') === '50') {
          await new Promise(r => { releaseStale = r })
          return HttpResponse.json({ sessions: [{ id: 'stale-page', created_at: '2026-01-01T00:00:00Z' }], total: 100 })
        }
        const sessions = [{ id: include ? 'with-sched' : 'first-page', created_at: '2026-01-01T00:00:00Z' }]
        return HttpResponse.json({ sessions, total: include ? 1 : 100 })
      })
    )
    render(Sessions)
    await waitFor(() => expect(screen.getByText('Load more')).toBeInTheDocument())
    await fireEvent.click(screen.getByText('Load more'))
    await waitFor(() => expect(releaseStale).toBeTypeOf('function'))

    await fireEvent.click(screen.getByLabelText('Show scheduled runs'))
    await waitFor(() => expect(screen.getByText('with-sched')).toBeInTheDocument())
    releaseStale()
    await new Promise(r => setTimeout(r, 20))
    expect(screen.queryByText('stale-page')).not.toBeInTheDocument()
  })
})
