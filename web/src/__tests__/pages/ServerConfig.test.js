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
    expect(screen.getByText('Enabled')).toBeInTheDocument()
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
})
