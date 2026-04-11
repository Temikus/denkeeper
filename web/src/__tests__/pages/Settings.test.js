import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Settings from '../../pages/Settings.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Settings page', () => {
  test('renders page title', async () => {
    render(Settings)
    expect(screen.getByText('Settings')).toBeInTheDocument()
  })

  test('renders auth status with pills', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Password Enabled')).toBeInTheDocument()
      expect(screen.getByText('OIDC Disabled')).toBeInTheDocument()
      expect(screen.getByText('Sessions Tracked')).toBeInTheDocument()
    })
  })

  test('shows active session count', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('2 active sessions')).toBeInTheDocument()
    })
  })

  test('renders session table with data', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('192.168.1.10')).toBeInTheDocument()
      expect(screen.getByText('10.0.0.1')).toBeInTheDocument()
    })
  })

  test('shows user agent in session table', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('curl/8.4.0')).toBeInTheDocument()
    })
  })

  test('revoke button shows confirm then revokes', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('192.168.1.10')).toBeInTheDocument()
    })

    // Click first Revoke button
    const revokeButtons = screen.getAllByText('Revoke')
    await fireEvent.click(revokeButtons[0])

    // Should show Confirm and Cancel
    await waitFor(() => {
      expect(screen.getByText('Confirm')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Confirm'))

    // Session should be removed from the table
    await waitFor(() => {
      expect(screen.queryByText('192.168.1.10')).not.toBeInTheDocument()
    })

    expect(screen.getByText('Session revoked')).toBeInTheDocument()
  })

  test('cancel revoke returns to normal state', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('192.168.1.10')).toBeInTheDocument()
    })

    const revokeButtons = screen.getAllByText('Revoke')
    await fireEvent.click(revokeButtons[0])

    await waitFor(() => {
      expect(screen.getByText('Cancel')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Cancel'))

    // Should return to showing Revoke buttons
    await waitFor(() => {
      const btns = screen.getAllByText('Revoke')
      expect(btns.length).toBeGreaterThanOrEqual(2)
    })
  })

  test('revoke all sessions button with confirm', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Revoke All Sessions')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Revoke All Sessions'))

    await waitFor(() => {
      expect(screen.getByText('Confirm Revoke All')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Confirm Revoke All'))

    await waitFor(() => {
      expect(screen.getByText(/Revoked 2 sessions/)).toBeInTheDocument()
    })
  })

  test('empty sessions shows empty state', async () => {
    server.use(
      http.get('/api/v1/auth/sessions', () => HttpResponse.json([]))
    )

    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('No active sessions to display.')).toBeInTheDocument()
    })
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/auth/status', () =>
        HttpResponse.json({ error: 'Auth status failed' }, { status: 500 })
      )
    )

    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Auth status failed')).toBeInTheDocument()
    })
  })

  test('loading state shows initially', () => {
    render(Settings)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  test('collapsible sections toggle', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Password Enabled')).toBeInTheDocument()
    })

    // Click Authentication section toggle to collapse
    const authToggle = screen.getByText('Authentication')
    await fireEvent.click(authToggle.closest('button'))

    // Pills should be hidden
    await waitFor(() => {
      expect(screen.queryByText('Password Enabled')).not.toBeInTheDocument()
    })

    // Click again to expand
    await fireEvent.click(authToggle.closest('button'))
    await waitFor(() => {
      expect(screen.getByText('Password Enabled')).toBeInTheDocument()
    })
  })
})
