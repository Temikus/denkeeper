import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { get } from 'svelte/store'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Login from '../../pages/Login.svelte'

beforeEach(() => {
  token.clear()
  authMode.set(null)
})

describe('Login page', () => {
  test('shows loading state initially', () => {
    // Override to delay response
    server.use(
      http.get('/auth/config', () => new Promise(() => {})),
      http.get('/api/v1/setup', () => new Promise(() => {})),
    )
    render(Login)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  test('transitions to login mode with password enabled', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false, account_setup_available: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('Sign in to access the dashboard.')).toBeInTheDocument()
    })
    // Password tab should be visible
    expect(screen.getByText('Password')).toBeInTheDocument()
    expect(screen.getByText('API Key')).toBeInTheDocument()
  })

  test('transitions to setup mode when setup required', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: false, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: true, account_setup_available: true })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('Welcome to Denkeeper')).toBeInTheDocument()
    })
    expect(screen.getByText('Create Account')).toBeInTheDocument()
    expect(screen.getByText('Create API Key')).toBeInTheDocument()
  })

  test('password login success sets authMode to session', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Password')).toBeInTheDocument()
    })

    await fireEvent.input(screen.getByPlaceholderText('Password'), { target: { value: 'correct' } })
    await fireEvent.click(screen.getByText('Sign in'))

    await waitFor(() => {
      expect(get(authMode)).toBe('session')
    })
  })

  test('password login error shows error message', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Password')).toBeInTheDocument()
    })

    await fireEvent.input(screen.getByPlaceholderText('Password'), { target: { value: 'wrong' } })
    await fireEvent.click(screen.getByText('Sign in'))

    await waitFor(() => {
      expect(screen.getByText('Invalid password')).toBeInTheDocument()
    })
  })

  test('password login empty validation', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Password')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Sign in'))

    await waitFor(() => {
      expect(screen.getByText('Password is required.')).toBeInTheDocument()
    })
  })

  test('API key login success sets token store', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('API Key')).toBeInTheDocument()
    })

    // Switch to API Key tab
    await fireEvent.click(screen.getByText('API Key'))
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/API key/)).toBeInTheDocument()
    })

    await fireEvent.input(screen.getByPlaceholderText(/API key/), { target: { value: 'dk_testkey123' } })
    await fireEvent.click(screen.getByText('Sign in'))

    await waitFor(() => {
      expect(get(token)).toBe('dk_testkey123')
    })
  })

  test('API key login error shows error message', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false })
      ),
      // Override agents to return 401 for invalid key
      http.get('/api/v1/agents', () =>
        new HttpResponse(null, { status: 401 })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('API Key')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('API Key'))
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/API key/)).toBeInTheDocument()
    })

    await fireEvent.input(screen.getByPlaceholderText(/API key/), { target: { value: 'dk_badkey' } })
    await fireEvent.click(screen.getByText('Sign in'))

    await waitFor(() => {
      expect(screen.getByText('Invalid API key or insufficient scopes.')).toBeInTheDocument()
    })
  })

  test('API key empty validation', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('API Key')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('API Key'))
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/API key/)).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Sign in'))

    await waitFor(() => {
      expect(screen.getByText('API key is required.')).toBeInTheDocument()
    })
  })

  test('setup flow creates API key and shows reveal', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: false, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: true, account_setup_available: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('Welcome to Denkeeper')).toBeInTheDocument()
    })

    // The API key setup form should be shown by default
    expect(screen.getByText('Create key')).toBeInTheDocument()
    await fireEvent.click(screen.getByText('Create key'))

    await waitFor(() => {
      expect(screen.getByText('Your API key')).toBeInTheDocument()
      expect(screen.getByText('dk_setup123')).toBeInTheDocument()
    })
  })

  test('reveal screen proceed to login pre-fills key', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: false, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: true, account_setup_available: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('Create key')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Create key'))
    await waitFor(() => {
      expect(screen.getByText('Log in with this key')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Log in with this key'))
    await waitFor(() => {
      expect(screen.getByText('Sign in to access the dashboard.')).toBeInTheDocument()
    })
  })

  test('SSO button is shown when OIDC enabled', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: true })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('Sign in with SSO')).toBeInTheDocument()
    })
  })

  test('method tab switching between Password and API Key', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: true, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: false })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Password')).toBeInTheDocument()
    })

    // Switch to API Key
    await fireEvent.click(screen.getByText('API Key'))
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/API key/)).toBeInTheDocument()
    })

    // Switch back to Password
    await fireEvent.click(screen.getByText('Password'))
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Password')).toBeInTheDocument()
    })
  })

  test('account setup password mismatch shows error', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: false, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: true, account_setup_available: true })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('Create Account')).toBeInTheDocument()
    })

    const pinInput = screen.getByPlaceholderText(/PIN/)
    const passwordInput = screen.getByPlaceholderText(/Choose a password/)
    const confirmInput = screen.getByPlaceholderText(/Confirm/)

    await fireEvent.input(pinInput, { target: { value: '123456' } })
    await fireEvent.input(passwordInput, { target: { value: 'password123' } })
    await fireEvent.input(confirmInput, { target: { value: 'different456' } })

    await fireEvent.click(screen.getByText('Create account'))

    await waitFor(() => {
      expect(screen.getByText('Passwords do not match.')).toBeInTheDocument()
    })
  })

  test('account setup short password shows error', async () => {
    server.use(
      http.get('/auth/config', () =>
        HttpResponse.json({ password_enabled: false, oidc_enabled: false })
      ),
      http.get('/api/v1/setup', () =>
        HttpResponse.json({ setup_required: true, account_setup_available: true })
      ),
    )

    render(Login)
    await waitFor(() => {
      expect(screen.getByText('Create Account')).toBeInTheDocument()
    })

    const pinInput = screen.getByPlaceholderText(/PIN/)
    const passwordInput = screen.getByPlaceholderText(/Choose a password/)
    const confirmInput = screen.getByPlaceholderText(/Confirm/)

    await fireEvent.input(pinInput, { target: { value: '123456' } })
    await fireEvent.input(passwordInput, { target: { value: 'short' } })
    await fireEvent.input(confirmInput, { target: { value: 'short' } })

    await fireEvent.click(screen.getByText('Create account'))

    await waitFor(() => {
      expect(screen.getByText('Password must be at least 8 characters.')).toBeInTheDocument()
    })
  })
})
