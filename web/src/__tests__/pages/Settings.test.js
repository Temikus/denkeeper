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

  test('shows "(this session)" badge on current session row', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('(this session)')).toBeInTheDocument()
    })
    // The badge should be next to the first session (sess_abc123), not the second
    const badges = screen.getAllByText('(this session)')
    expect(badges).toHaveLength(1)
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

  // --- Section 2: Password Management ---

  test('password section shows form when password enabled', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Current password')).toBeInTheDocument()
      expect(screen.getByText('Change Password')).toBeInTheDocument()
    })
  })

  test('password section shows not configured when disabled', async () => {
    server.use(
      http.get('/api/v1/auth/status', () => HttpResponse.json({
        password_enabled: false,
        oidc_enabled: false,
        sessions_trackable: true,
        active_session_count: 0,
        preferred_login_method: 'auto',
      }))
    )

    render(Settings)
    await waitFor(() => {
      expect(screen.getByText(/Password login is not configured/)).toBeInTheDocument()
    })
  })

  test('password strength indicator shows levels', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Change Password')).toBeInTheDocument()
    })

    const inputs = document.querySelectorAll('input[type="password"]')
    // inputs[1] is the "New password" field
    await fireEvent.input(inputs[1], { target: { value: 'short' } })
    await waitFor(() => {
      expect(screen.getByText('Too short')).toBeInTheDocument()
    })

    await fireEvent.input(inputs[1], { target: { value: 'eightchr' } })
    await waitFor(() => {
      expect(screen.getByText('OK')).toBeInTheDocument()
    })

    await fireEvent.input(inputs[1], { target: { value: 'twelvecharss' } })
    await waitFor(() => {
      expect(screen.getByText('Strong')).toBeInTheDocument()
    })

    await fireEvent.input(inputs[1], { target: { value: 'sixteencharacter' } })
    await waitFor(() => {
      expect(screen.getByText('Very strong')).toBeInTheDocument()
    })
  })

  test('password change mismatch shows error', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Change Password')).toBeInTheDocument()
    })

    const inputs = document.querySelectorAll('input[type="password"]')
    await fireEvent.input(inputs[0], { target: { value: 'correct' } })
    await fireEvent.input(inputs[1], { target: { value: 'newpass1234' } })
    await fireEvent.input(inputs[2], { target: { value: 'mismatch' } })
    await fireEvent.click(screen.getByText('Change Password'))

    await waitFor(() => {
      expect(screen.getByText('Passwords do not match')).toBeInTheDocument()
    })
  })

  test('password change success shows banner', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Change Password')).toBeInTheDocument()
    })

    const inputs = document.querySelectorAll('input[type="password"]')
    await fireEvent.input(inputs[0], { target: { value: 'correct' } })
    await fireEvent.input(inputs[1], { target: { value: 'newpass1234' } })
    await fireEvent.input(inputs[2], { target: { value: 'newpass1234' } })
    await fireEvent.click(screen.getByText('Change Password'))

    await waitFor(() => {
      expect(screen.getByText('Password changed successfully')).toBeInTheDocument()
    })
  })

  // --- Section 3: OIDC Status ---

  test('OIDC section shows not configured when disabled', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText(/OIDC is not configured/)).toBeInTheDocument()
    })
  })

  test('OIDC section shows issuer and test button when enabled', async () => {
    server.use(
      http.get('/api/v1/auth/status', () => HttpResponse.json({
        password_enabled: true,
        oidc_enabled: true,
        sessions_trackable: true,
        active_session_count: 2,
        oidc_issuer: 'https://accounts.example.com',
        oidc_allowed_emails: ['user@example.com'],
        preferred_login_method: 'auto',
      }))
    )

    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('https://accounts.example.com')).toBeInTheDocument()
      expect(screen.getByText('user@example.com')).toBeInTheDocument()
      expect(screen.getByText('Test Connection')).toBeInTheDocument()
    })
  })

  test('OIDC test connection shows success', async () => {
    server.use(
      http.get('/api/v1/auth/status', () => HttpResponse.json({
        password_enabled: true,
        oidc_enabled: true,
        sessions_trackable: true,
        active_session_count: 2,
        oidc_issuer: 'https://accounts.example.com',
        oidc_allowed_emails: ['user@example.com'],
        preferred_login_method: 'auto',
      }))
    )

    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Test Connection')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Test Connection'))
    await waitFor(() => {
      expect(screen.getByText(/Connection successful/)).toBeInTheDocument()
    })
  })

  // --- Section 5: Login Preferences ---

  test('preferences section renders dropdown', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Login Preferences')).toBeInTheDocument()
      const select = document.querySelector('#pref-login')
      expect(select).toBeInTheDocument()
      expect(select.value).toBe('auto')
    })
  })

  test('preferences save calls API', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Login Preferences')).toBeInTheDocument()
    })

    const select = document.querySelector('#pref-login')
    await fireEvent.change(select, { target: { value: 'password' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(screen.getByText('Login preference saved')).toBeInTheDocument()
    })
  })

  test('OIDC test button shows error inline', async () => {
    server.use(
      http.get('/api/v1/auth/status', () => HttpResponse.json({
        password_enabled: true,
        oidc_enabled: true,
        sessions_trackable: true,
        active_session_count: 1,
        oidc_issuer: 'https://accounts.example.com',
        oidc_allowed_emails: ['user@example.com'],
        preferred_login_method: 'auto',
      })),
      http.get('/api/v1/auth/oidc/test', () => HttpResponse.json({
        ok: false,
        error: 'connection refused',
      })),
    )

    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Test Connection')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Test Connection'))
    await waitFor(() => {
      expect(screen.getByText(/connection refused/)).toBeInTheDocument()
    })
  })

  test('shows version info when server returns version', async () => {
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('v0.25.0')).toBeInTheDocument()
      expect(screen.getByText('abc1234')).toBeInTheDocument()
      expect(screen.getByText('go1.22.0')).toBeInTheDocument()
    })
  })

  test('hides commit when commit is unknown', async () => {
    server.use(
      http.get('/api/v1/server/config', () => HttpResponse.json({
        version: 'v0.25.0',
        commit: 'unknown',
        go_version: 'go1.22.0',
      }))
    )
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('v0.25.0')).toBeInTheDocument()
    })
    // commit='unknown' should not be rendered
    expect(screen.queryByText('unknown')).not.toBeInTheDocument()
  })

  test('hides version info when server config fails', async () => {
    server.use(
      http.get('/api/v1/server/config', () => HttpResponse.json({ error: 'forbidden' }, { status: 403 }))
    )
    render(Settings)
    await waitFor(() => {
      expect(screen.getByText('Password Enabled')).toBeInTheDocument()
    })
    expect(screen.queryByText('Version')).not.toBeInTheDocument()
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
