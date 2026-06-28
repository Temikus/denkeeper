import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Schedules from '../../pages/Schedules.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Schedules page', () => {
  test('renders page title and add button', async () => {
    render(Schedules)
    expect(screen.getByText('Schedules')).toBeInTheDocument()
    expect(screen.getByText('+ Add Schedule')).toBeInTheDocument()
  })

  test('renders schedule table with data', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'daily-check', expression: '0 9 * * *', skill: 'report', channel: 'telegram:123', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('daily-check')).toBeInTheDocument()
      expect(screen.getByText('0 9 * * *')).toBeInTheDocument()
      expect(screen.getByText('yes')).toBeInTheDocument()
    })
  })

  test('shows empty state when no schedules', async () => {
    server.use(
      http.get('/api/v1/schedules', () => HttpResponse.json([]))
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText(/No schedules configured/)).toBeInTheDocument()
    })
  })

  test('add button opens inline form', async () => {
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('+ Add Schedule')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Schedule'))
    await waitFor(() => {
      expect(screen.getByText('Add Schedule', { selector: 'h2' })).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/daily-report/)).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/@daily/)).toBeInTheDocument()
    })
  })

  test('agent filter refetches the list scoped to one agent via the API', async () => {
    const all = [
      { name: 'alice-job', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
      { name: 'bob-job', expression: '@daily', agent: 'bob', channel: 'telegram:2', enabled: true },
    ]
    let lastAgentParam = '__unset__'
    server.use(
      // Mirror the backend: honour the ?agent= query parameter.
      http.get('/api/v1/schedules', ({ request }) => {
        const agent = new URL(request.url).searchParams.get('agent')
        lastAgentParam = agent
        return HttpResponse.json(agent ? all.filter(s => s.agent === agent) : all)
      })
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('alice-job')).toBeInTheDocument()
      expect(screen.getByText('bob-job')).toBeInTheDocument()
    })

    // Filter dropdown appears because there is more than one owning agent.
    const filter = screen.getByTestId('agent-filter')
    await fireEvent.change(filter, { target: { value: 'alice' } })

    await waitFor(() => {
      expect(screen.getByText('alice-job')).toBeInTheDocument()
      expect(screen.queryByText('bob-job')).not.toBeInTheDocument()
    })
    // The narrowing came from a server round-trip carrying ?agent=alice.
    expect(lastAgentParam).toBe('alice')

    // Switching back to "All agents" refetches without the filter.
    await fireEvent.change(filter, { target: { value: '' } })
    await waitFor(() => {
      expect(screen.getByText('bob-job')).toBeInTheDocument()
    })
    expect(lastAgentParam).toBeNull()
  })

  test('filter control survives an empty filtered result', async () => {
    const all = [
      { name: 'alice-job', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
      { name: 'bob-job', expression: '@daily', agent: 'bob', channel: 'telegram:2', enabled: true },
    ]
    server.use(
      http.get('/api/v1/schedules', ({ request }) => {
        const agent = new URL(request.url).searchParams.get('agent')
        // Simulate an agent with no schedules.
        return HttpResponse.json(agent === 'alice' ? [] : all)
      })
    )

    render(Schedules)
    await waitFor(() => expect(screen.getByText('bob-job')).toBeInTheDocument())

    const filter = screen.getByTestId('agent-filter')
    await fireEvent.change(filter, { target: { value: 'alice' } })

    await waitFor(() => {
      expect(screen.getByText(/No schedules for alice/)).toBeInTheDocument()
    })
    // The filter dropdown must remain so the user can recover.
    expect(screen.getByTestId('agent-filter')).toBeInTheDocument()
  })

  test('no agent filter shown for a single-agent list', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'solo', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('solo')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('agent-filter')).not.toBeInTheDocument()
  })

  test('edit button opens pre-filled form', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'daily-check', expression: '0 9 * * *', channel: 'telegram:123', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText('Edit Schedule')).toBeInTheDocument()
    })
  })

  test('delete shows confirmation modal', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'daily-check', expression: '0 9 * * *', channel: 'telegram:123', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      // Find the delete button in the actions column
      const deleteBtn = document.querySelector('.btn-sm.danger')
      expect(deleteBtn).toBeInTheDocument()
    })

    const deleteBtn = document.querySelector('.btn-sm.danger')
    await fireEvent.click(deleteBtn)
    await waitFor(() => {
      expect(screen.getByText('Delete Schedule')).toBeInTheDocument()
    })
    const modal = document.querySelector('.confirm-modal')
    expect(modal.textContent).toContain('daily-check')
  })

  test('channel and agent fields render as dropdowns with known options', async () => {
    server.use(
      http.get('/api/v1/channels', () =>
        HttpResponse.json([
          { name: 'work', agent: 'default', implicit: false },
          { name: 'auto-gen', agent: 'default', implicit: true },
        ])
      ),
      http.get('/api/v1/agents', () =>
        HttpResponse.json([
          { name: 'default' },
          { name: 'helper' },
        ])
      )
    )

    render(Schedules)

    // Wait for data to load (loading spinner disappears or table/empty state renders)
    await waitFor(() => {
      expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Schedule'))
    await waitFor(() => {
      const selects = document.querySelectorAll('[data-testid="schedule-form"] select')
      expect(selects.length).toBeGreaterThanOrEqual(2)
    })

    const channelSelect = screen.getByTestId('channel-select')
    const options = Array.from(channelSelect.options).map(o => o.textContent)
    expect(options).toContain('work (default)')
    expect(options).not.toContain('auto-gen (default)')
    expect(options).toContain('Custom...')

    const agentSelect = screen.getByTestId('agent-select')
    const agentOptions = Array.from(agentSelect.options).map(o => o.textContent)
    expect(agentOptions).toContain('default')
    expect(agentOptions).toContain('helper')
    expect(agentOptions).toContain('Custom...')
  })

  test('selecting Custom reveals text input for channel', async () => {
    server.use(
      http.get('/api/v1/channels', () =>
        HttpResponse.json([
          { name: 'work', agent: 'default', implicit: false },
        ])
      )
    )

    render(Schedules)

    await waitFor(() => {
      expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Schedule'))
    await waitFor(() => {
      expect(screen.getByText('Add Schedule', { selector: 'h2' })).toBeInTheDocument()
    })

    // With channels loaded, dropdown defaults to first channel — no custom input yet
    expect(screen.queryByPlaceholderText('channel name')).not.toBeInTheDocument()

    const channelSelect = screen.getByTestId('channel-select')
    await fireEvent.change(channelSelect, { target: { value: '__custom__' } })

    await waitFor(() => {
      expect(screen.getByPlaceholderText('channel name')).toBeInTheDocument()
    })
  })

  test('selecting Custom reveals text input for agent', async () => {
    render(Schedules)

    await waitFor(() => {
      expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Schedule'))
    await waitFor(() => {
      expect(screen.getByText('Add Schedule', { selector: 'h2' })).toBeInTheDocument()
    })

    // Default fixtures include agents, so dropdown defaults to first agent
    expect(screen.queryByPlaceholderText('agent name')).not.toBeInTheDocument()

    const agentSelect = screen.getByTestId('agent-select')
    await fireEvent.change(agentSelect, { target: { value: '__custom__' } })

    await waitFor(() => {
      expect(screen.getByPlaceholderText('agent name')).toBeInTheDocument()
    })
  })

  test('edit pre-selects known channel and falls back to custom for unknown', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'sched-known', expression: '@daily', channel: 'work', agent: 'default', enabled: true },
        ])
      ),
      http.get('/api/v1/channels', () =>
        HttpResponse.json([
          { name: 'work', agent: 'default', implicit: false },
        ])
      ),
      http.get('/api/v1/agents', () =>
        HttpResponse.json([
          { name: 'default' },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText('Edit Schedule')).toBeInTheDocument()
    })

    const channelSelect = screen.getByTestId('channel-select')
    expect(channelSelect.value).toBe('work')
    expect(screen.queryByPlaceholderText('channel name')).not.toBeInTheDocument()
  })

  test('shows warning when channels or agents fail to load', async () => {
    server.use(
      http.get('/api/v1/channels', () =>
        HttpResponse.json({ error: 'forbidden' }, { status: 403 })
      ),
      http.get('/api/v1/agents', () =>
        HttpResponse.json({ error: 'forbidden' }, { status: 403 })
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
    })

    expect(screen.getByTestId('load-warning')).toBeInTheDocument()
    expect(screen.getByTestId('load-warning').textContent).toContain('channels and agents')
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json({ error: 'Server error' }, { status: 500 })
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('Server error')).toBeInTheDocument()
    })
  })
})
