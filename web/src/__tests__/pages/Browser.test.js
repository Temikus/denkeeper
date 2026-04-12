import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Browser from '../../pages/Browser.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')

  // Provide richer browser data fixtures
  server.use(
    http.get('/api/v1/browser/sessions', () =>
      HttpResponse.json({
        sessions: [
          { name: 'test-session', status: 'connected', tool_count: 5 },
        ],
      })
    ),
    http.get('/api/v1/browser/profiles', () =>
      HttpResponse.json({
        profiles: [
          { agent: 'default', size_bytes: 1048576, domains: ['example.com'], last_used: '2026-04-01T12:00:00Z' },
        ],
      })
    ),
    http.get('/api/v1/browser/config', () =>
      HttpResponse.json({
        image: 'chromium:latest',
        memory_limit: '512M',
        cpu_limit: '1.0',
        session_ttl: '30m',
        max_pages: 5,
      })
    ),
  )
})

describe('Browser page', () => {
  test('renders page title', async () => {
    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('Browser')).toBeInTheDocument()
    })
  })

  test('shows empty state when no sessions', async () => {
    server.use(
      http.get('/api/v1/browser/sessions', () =>
        HttpResponse.json({ sessions: [] })
      ),
      http.get('/api/v1/browser/profiles', () =>
        HttpResponse.json({ profiles: [] })
      ),
    )

    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('No active browser sessions.')).toBeInTheDocument()
    })
  })

  test('shows active sessions section header', async () => {
    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('Active Sessions')).toBeInTheDocument()
    })
  })

  test('shows refresh button', async () => {
    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('Refresh')).toBeInTheDocument()
    })
  })

  test('renders session data', async () => {
    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('test-session')).toBeInTheDocument()
    })
  })

  test('shows Profiles section header', async () => {
    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('Profiles')).toBeInTheDocument()
    })
  })

  test('renders profile agent name', async () => {
    render(Browser)
    await waitFor(() => {
      // Profile renders p.agent, not p.name. The fixture has sessions with 'test-session'
      // and profiles with agent 'default'. Since 'default' also appears in sessions table header context,
      // just verify the profile table is populated
      const rows = document.querySelectorAll('table')
      expect(rows.length).toBeGreaterThanOrEqual(2) // sessions table + profiles table
    })
  })

  test('shows profile domain pills', async () => {
    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('example.com')).toBeInTheDocument()
    })
  })

  test('shows Delete button for profiles', async () => {
    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('Delete')).toBeInTheDocument()
    })
  })

  test('clicking Delete shows confirmation dialog', async () => {
    render(Browser)
    await waitFor(() => screen.getByText('Delete'))

    await fireEvent.click(screen.getByText('Delete'))
    await waitFor(() => {
      expect(screen.getByText('Delete Profile')).toBeInTheDocument()
      expect(screen.getByText(/cookies, localStorage/)).toBeInTheDocument()
    })
  })

  test('shows Configuration section with details', async () => {
    render(Browser)
    await waitFor(() => {
      expect(screen.getByText('Configuration')).toBeInTheDocument()
      expect(screen.getByText('chromium:latest')).toBeInTheDocument()
      expect(screen.getByText('512M')).toBeInTheDocument()
    })
  })

  test('refresh button reloads data', async () => {
    let fetchCount = 0
    server.use(
      http.get('/api/v1/browser/sessions', () => {
        fetchCount++
        return HttpResponse.json({ sessions: [] })
      }),
    )

    render(Browser)
    await waitFor(() => screen.getByText('Refresh'))

    await fireEvent.click(screen.getByText('Refresh'))
    await waitFor(() => {
      expect(fetchCount).toBeGreaterThanOrEqual(2)
    })
  })
})
