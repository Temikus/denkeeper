import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Browser from '../../pages/Browser.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
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
})
