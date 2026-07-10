import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import ServerConfig from '../../pages/ServerConfig.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('ServerConfig page', () => {
  test('shows loading state initially', () => {
    server.use(
      http.get('/api/v1/server/config', () => new Promise(() => {})),
    )
    render(ServerConfig)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  test('renders server config data after load', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Listen Address')).toBeInTheDocument()
    })
    expect(screen.getByText(':8080')).toBeInTheDocument()
    expect(screen.getAllByText('Disabled').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('100 req/s')).toBeInTheDocument()
    expect(screen.getByText('https://example.com')).toBeInTheDocument()
  })

  test('renders WebSocket section', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('WebSocket')).toBeInTheDocument()
    })
    expect(screen.getAllByText('Enabled').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('50')).toBeInTheDocument()
    expect(screen.getByText('5m')).toBeInTheDocument()
  })

  test('renders External Access section with external_url', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('External URL')).toBeInTheDocument()
    })
    expect(screen.getByText('https://den.example.com')).toBeInTheDocument()
  })

  test('shows Auto-detect when external_url is empty', async () => {
    server.use(
      http.get('/api/v1/server/config', () => HttpResponse.json({
        listen: ':8080',
        tls: false,
        rate_limit: 0,
        cors_origins: [],
        websocket_enabled: false,
        websocket_max_connections: 0,
        websocket_replay_buffer_ttl: '',
        external_url: '',
        timezone: 'UTC',
      })),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Auto-detect')).toBeInTheDocument()
    })
    // Both rate limit and max connections show "Unlimited" when 0
    expect(screen.getAllByText('Unlimited').length).toBeGreaterThanOrEqual(1)
    // TLS Disabled and WebSocket Disabled both appear
    expect(screen.getAllByText('Disabled').length).toBeGreaterThanOrEqual(1)
  })

  test('Edit button shows input form', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getAllByText('Edit').length).toBeGreaterThanOrEqual(1)
    })

    const editButtons = screen.getAllByText('Edit')
    // Click the External URL edit button (last one)
    await fireEvent.click(editButtons[editButtons.length - 1])
    expect(screen.getByRole('textbox')).toBeInTheDocument()
    expect(screen.getByText('Save')).toBeInTheDocument()
    expect(screen.getByText('Cancel')).toBeInTheDocument()
  })

  test('Cancel returns to view mode', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getAllByText('Edit').length).toBeGreaterThanOrEqual(1)
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[editButtons.length - 1])
    expect(screen.getByRole('textbox')).toBeInTheDocument()

    await fireEvent.click(screen.getByText('Cancel'))
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.getAllByText('Edit').length).toBeGreaterThanOrEqual(1)
  })

  test('Save calls PATCH and shows success feedback', async () => {
    vi.useFakeTimers()
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getAllByText('Edit').length).toBeGreaterThanOrEqual(1)
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[editButtons.length - 1])
    const input = screen.getByRole('textbox')
    await fireEvent.input(input, { target: { value: 'https://new.example.com' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(screen.getByText('Saved')).toBeInTheDocument()
    })
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()

    vi.useRealTimers()
  })

  test('renders General section with timezone', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('General')).toBeInTheDocument()
    })
    expect(screen.getByText('Timezone')).toBeInTheDocument()
    expect(screen.getByText('UTC')).toBeInTheDocument()
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/server/config', () =>
        HttpResponse.json({ error: 'Internal server error' }, { status: 500 })
      ),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Internal server error')).toBeInTheDocument()
    })
  })

  test('save error shows ErrorBanner', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getAllByText('Edit').length).toBeGreaterThanOrEqual(1)
    })

    server.use(
      http.patch('/api/v1/server/config', () =>
        HttpResponse.json({ error: 'Save failed' }, { status: 500 })
      ),
    )

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[editButtons.length - 1])
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(screen.getByText('Save failed')).toBeInTheDocument()
    })
  })

  test('timezone edit opens picker and saves', async () => {
    vi.useFakeTimers()
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Timezone')).toBeInTheDocument()
    })

    // Click Edit on timezone row
    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[0])

    // Timezone filter input should appear
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Filter timezones...')).toBeInTheDocument()
    })

    // Select element should be visible
    const select = document.querySelector('.tz-select')
    expect(select).toBeInTheDocument()

    // Save button should be visible
    expect(screen.getByText('Save')).toBeInTheDocument()

    // Click Save
    await fireEvent.click(screen.getByText('Save'))
    await waitFor(() => {
      expect(screen.getByText(/Saved/)).toBeInTheDocument()
    })

    vi.useRealTimers()
  })

  test('timezone cancel returns to view mode', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Timezone')).toBeInTheDocument()
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[0])
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Filter timezones...')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Cancel'))
    expect(screen.queryByPlaceholderText('Filter timezones...')).not.toBeInTheDocument()
  })

  test('timezone custom input toggle works', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Timezone')).toBeInTheDocument()
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[0])
    await waitFor(() => {
      expect(screen.getByText('Enter custom value')).toBeInTheDocument()
    })

    // Switch to custom input
    await fireEvent.click(screen.getByText('Enter custom value'))
    expect(screen.getByPlaceholderText('e.g. America/New_York')).toBeInTheDocument()

    // Switch back to list
    await fireEvent.click(screen.getByText('Back to list'))
    expect(screen.getByPlaceholderText('Filter timezones...')).toBeInTheDocument()
  })

  test('timezone filter narrows options', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Timezone')).toBeInTheDocument()
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[0])
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Filter timezones...')).toBeInTheDocument()
    })

    const filter = screen.getByPlaceholderText('Filter timezones...')
    await fireEvent.input(filter, { target: { value: 'Tokyo' } })

    // Should show only matching timezone(s)
    const options = document.querySelectorAll('.tz-select option')
    expect(options.length).toBeGreaterThan(0)
    const texts = Array.from(options).map(o => o.textContent)
    expect(texts.some(t => t.includes('Tokyo'))).toBe(true)
  })

  test('reload config button works', async () => {
    vi.useFakeTimers()
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Reload')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Reload'))
    await waitFor(() => {
      expect(screen.getByText('Config reloaded')).toBeInTheDocument()
    })

    vi.useRealTimers()
  })

  test('reload config error shows ErrorBanner', async () => {
    server.use(
      http.post('/api/v1/server/reload', () =>
        HttpResponse.json({ error: 'Reload failed' }, { status: 500 })
      ),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Reload')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Reload'))
    await waitFor(() => {
      expect(screen.getByText('Reload failed')).toBeInTheDocument()
    })
  })

  test('restart shows confirm then executes', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Restart')).toBeInTheDocument()
    })

    // Click Restart to show confirmation
    await fireEvent.click(screen.getByText('Restart'))
    expect(screen.getByText('Confirm Restart')).toBeInTheDocument()
    expect(screen.getByText('Cancel')).toBeInTheDocument()
  })

  test('restart cancel hides confirmation', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Restart')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Restart'))
    expect(screen.getByText('Confirm Restart')).toBeInTheDocument()

    await fireEvent.click(screen.getByText('Cancel'))
    // Should be back to showing Restart button
    expect(screen.getByText('Restart')).toBeInTheDocument()
    expect(screen.queryByText('Confirm Restart')).not.toBeInTheDocument()
  })

  test('restart error shows ErrorBanner', async () => {
    server.use(
      http.post('/api/v1/server/restart', () =>
        HttpResponse.json({ error: 'Restart failed' }, { status: 500 })
      ),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Restart')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Restart'))
    await fireEvent.click(screen.getByText('Confirm Restart'))

    await waitFor(() => {
      expect(screen.getByText('Restart failed')).toBeInTheDocument()
    })
  })

  test('timezone save error shows ErrorBanner', async () => {
    server.use(
      http.patch('/api/v1/server/config', () =>
        HttpResponse.json({ error: 'TZ save failed' }, { status: 500 })
      ),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Timezone')).toBeInTheDocument()
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[0])
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Filter timezones...')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Save'))
    await waitFor(() => {
      expect(screen.getByText('TZ save failed')).toBeInTheDocument()
    })
  })

  test('MCP Server section shows Disabled by default', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('MCP Server')).toBeInTheDocument()
    })
    // MCP endpoint/transport/timeout cards should NOT be visible when disabled
    expect(screen.queryByText('Endpoint')).not.toBeInTheDocument()
    expect(screen.queryByText('Transport')).not.toBeInTheDocument()
  })

  test('MCP Server enabled shows endpoint, transport and timeout fields', async () => {
    server.use(
      http.get('/api/v1/server/config', () => HttpResponse.json({
        listen: ':8080',
        tls: false,
        rate_limit: 100,
        cors_origins: ['https://example.com'],
        websocket_enabled: true,
        websocket_max_connections: 50,
        websocket_replay_buffer_ttl: '5m',
        mcp_server_enabled: true,
        mcp_server_transport: 'streamable',
        mcp_server_session_timeout: '30m',
        mcp_server_chat_timeout: '2m',
        mcp_server_stateless: false,
        mcp_server_endpoint: 'http://localhost:8080/api/v1/mcp',
        external_url: 'https://den.example.com',
        timezone: 'UTC',
        version: 'v0.25.0',
        commit: 'abc1234',
        go_version: 'go1.22.0',
      })),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('Endpoint')).toBeInTheDocument()
    })
    expect(screen.getByText('http://localhost:8080/api/v1/mcp')).toBeInTheDocument()
    expect(screen.getByText('Copy')).toBeInTheDocument()
    expect(screen.getByText('Transport')).toBeInTheDocument()
    expect(screen.getByText('Session Timeout')).toBeInTheDocument()
    expect(screen.getByText('Chat Timeout')).toBeInTheDocument()
    expect(screen.getByText('Stateless')).toBeInTheDocument()
    expect(screen.getByText('Transport changes require a server restart to take effect.')).toBeInTheDocument()
  })

  test('MCP Server toggle calls PATCH', async () => {
    let patchCalled = false
    server.use(
      http.patch('/api/v1/server/config', async ({ request }) => {
        const body = await request.json()
        expect(body.mcp_server_enabled).toBe(true)
        patchCalled = true
        return HttpResponse.json({ ok: true })
      }),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('MCP Server')).toBeInTheDocument()
    })
    const checkbox = screen.getByRole('checkbox', { name: 'MCP server' })
    await fireEvent.change(checkbox, { target: { checked: true } })
    await waitFor(() => {
      expect(patchCalled).toBe(true)
    })
  })

  test('In-Process Tools section shows web and script toggles enabled', async () => {
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('In-Process Tools')).toBeInTheDocument()
    })
    expect(screen.getByText('Web tools (web_search / web_fetch)')).toBeInTheDocument()
    expect(screen.getByText('Deterministic compute (run_javascript)')).toBeInTheDocument()

    const webToggle = screen.getByRole('checkbox', { name: 'Web tools' })
    const scriptToggle = screen.getByRole('checkbox', { name: 'Deterministic compute' })
    expect(webToggle.checked).toBe(true)
    expect(scriptToggle.checked).toBe(true)
  })

  test('Web tools toggle PATCHes web_tools_enabled and shows restart hint', async () => {
    let body = null
    server.use(
      http.patch('/api/v1/server/config', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ status: 'updated', restart_required: true })
      }),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('In-Process Tools')).toBeInTheDocument()
    })

    const webToggle = screen.getByRole('checkbox', { name: 'Web tools' })
    await fireEvent.change(webToggle, { target: { checked: false } })

    await waitFor(() => {
      expect(body).toEqual({ web_tools_enabled: false })
    })
    await waitFor(() => {
      expect(screen.getByText(/Restart the server/)).toBeInTheDocument()
    })
  })

  test('Deterministic compute toggle PATCHes script_enabled', async () => {
    let body = null
    server.use(
      http.patch('/api/v1/server/config', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ status: 'updated', restart_required: true })
      }),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('In-Process Tools')).toBeInTheDocument()
    })

    const scriptToggle = screen.getByRole('checkbox', { name: 'Deterministic compute' })
    await fireEvent.change(scriptToggle, { target: { checked: false } })

    await waitFor(() => {
      expect(body).toEqual({ script_enabled: false })
    })
  })

  test('In-process tool toggle error shows ErrorBanner', async () => {
    server.use(
      http.patch('/api/v1/server/config', () =>
        HttpResponse.json({ error: 'Toggle failed' }, { status: 500 })
      ),
    )
    render(ServerConfig)
    await waitFor(() => {
      expect(screen.getByText('In-Process Tools')).toBeInTheDocument()
    })

    const webToggle = screen.getByRole('checkbox', { name: 'Web tools' })
    await fireEvent.change(webToggle, { target: { checked: false } })

    await waitFor(() => {
      expect(screen.getByText('Toggle failed')).toBeInTheDocument()
    })
  })
})
