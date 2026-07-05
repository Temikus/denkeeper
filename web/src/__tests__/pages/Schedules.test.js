import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte'
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

  test('renders grouped schedule table with data', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'daily-check', expression: '0 9 * * *', skill: 'report', agent: 'default', channel: 'telegram:123', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('daily-check')).toBeInTheDocument()
      expect(screen.getByText('0 9 * * *')).toBeInTheDocument()
      expect(screen.getByText('skill: report')).toBeInTheDocument()
    })
    expect(screen.getByTestId('agent-section-default')).toBeInTheDocument()
    expect(screen.getByLabelText('Toggle daily-check')).toBeChecked()
  })

  test('groups schedules into one section per agent, no Agent column', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'alice-job', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
          { name: 'bob-job', expression: '@daily', agent: 'bob', channel: 'telegram:2', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('agent-section-alice')).toBeInTheDocument()
      expect(screen.getByTestId('agent-section-bob')).toBeInTheDocument()
    })
    expect(within(screen.getByTestId('agent-section-alice')).getByText('alice-job')).toBeInTheDocument()
    expect(within(screen.getByTestId('agent-section-bob')).getByText('bob-job')).toBeInTheDocument()
    expect(within(screen.getByTestId('agent-section-alice')).getByText('1 schedule')).toBeInTheDocument()
    // Agent ownership lives in section headers now — no Agent table column.
    expect(screen.queryByRole('columnheader', { name: 'Agent' })).not.toBeInTheDocument()
  })

  test('section header shows the agent tier badge from the agents list', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'alice-job', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
        ])
      ),
      http.get('/api/v1/agents', () =>
        HttpResponse.json([{ name: 'alice', permission_tier: 'supervised' }])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('agent-section-alice')).toBeInTheDocument()
    })
    const header = screen.getByTestId('agent-section-alice').querySelector('.section-header')
    expect(header.textContent).toContain('supervised')
  })

  test('sections render without tier badges when agents fail to load', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'alice-job', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
        ])
      ),
      http.get('/api/v1/agents', () =>
        HttpResponse.json({ error: 'forbidden' }, { status: 403 })
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('agent-section-alice')).toBeInTheDocument()
    })
    expect(screen.getByTestId('agent-section-alice').querySelector('.tier-badge')).toBeNull()
  })

  test('row shows a tier badge only for a session_tier override', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'plain', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
          { name: 'override', expression: '@daily', agent: 'alice', channel: 'telegram:1', session_tier: 'restricted', enabled: true },
        ])
      ),
      http.get('/api/v1/agents', () => HttpResponse.json([]))
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('schedule-row-override')).toBeInTheDocument()
    })
    expect(within(screen.getByTestId('schedule-row-override')).getByText('restricted')).toBeInTheDocument()
    expect(screen.getByTestId('schedule-row-plain').querySelector('.tier-badge')).toBeNull()
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

  test('agent chips filter sections client-side without a refetch', async () => {
    let getCount = 0
    server.use(
      http.get('/api/v1/schedules', () => {
        getCount++
        return HttpResponse.json([
          { name: 'alice-job', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
          { name: 'bob-job', expression: '@daily', agent: 'bob', channel: 'telegram:2', enabled: true },
        ])
      })
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('alice-job')).toBeInTheDocument()
      expect(screen.getByText('bob-job')).toBeInTheDocument()
    })
    expect(getCount).toBe(1)

    await fireEvent.click(screen.getByTestId('agent-chip-alice'))
    await waitFor(() => {
      expect(screen.getByText('alice-job')).toBeInTheDocument()
      expect(screen.queryByText('bob-job')).not.toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText(/All agents/))
    await waitFor(() => {
      expect(screen.getByText('bob-job')).toBeInTheDocument()
    })
    // Narrowing never went back to the server.
    expect(getCount).toBe(1)
  })

  test('chips show per-agent schedule counts', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'alice-job', expression: '@daily', agent: 'alice', channel: 'telegram:1', enabled: true },
          { name: 'alice-job-2', expression: '@hourly', agent: 'alice', channel: 'telegram:1', enabled: true },
          { name: 'bob-job', expression: '@daily', agent: 'bob', channel: 'telegram:2', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('agent-filter')).toBeInTheDocument()
    })
    expect(screen.getByText('All agents (3)')).toBeInTheDocument()
    expect(screen.getByTestId('agent-chip-alice').textContent).toContain('alice (2)')
    expect(screen.getByTestId('agent-chip-bob').textContent).toContain('bob (1)')
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

  test('toggle PATCHes enabled and reflects the refetched state', async () => {
    let patchBody = null
    let toggled = false
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'daily-check', expression: '0 9 * * *', agent: 'default', channel: 'telegram:123', enabled: !toggled },
        ])
      ),
      http.patch('/api/v1/schedules/:name', async ({ request }) => {
        patchBody = await request.json()
        toggled = true
        return HttpResponse.json({ ok: true })
      })
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByLabelText('Toggle daily-check')).toBeChecked()
    })

    await fireEvent.click(screen.getByLabelText('Toggle daily-check'))
    await waitFor(() => {
      expect(patchBody).toEqual({ enabled: false })
      expect(screen.getByLabelText('Toggle daily-check')).not.toBeChecked()
    })
  })

  test('disabled schedule renders paused and dimmed, missing last run as Never', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'sleepy', expression: '@daily', agent: 'default', channel: 'telegram:1', enabled: false },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('schedule-row-sleepy')).toBeInTheDocument()
    })
    const row = screen.getByTestId('schedule-row-sleepy')
    expect(row.classList.contains('paused')).toBe(true)
    expect(within(row).getByText('Paused')).toBeInTheDocument()
    expect(within(row).getByText('Never')).toBeInTheDocument()
    expect(screen.getByLabelText('Toggle sleepy')).not.toBeChecked()
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
      expect(screen.getByLabelText('Edit daily-check')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByLabelText('Edit daily-check'))
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
      expect(screen.getByLabelText('Delete daily-check')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByLabelText('Delete daily-check'))
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
      expect(screen.getByLabelText('Edit sched-known')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByLabelText('Edit sched-known'))
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
