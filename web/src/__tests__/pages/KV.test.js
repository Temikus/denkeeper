import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import KV from '../../pages/KV.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('KV page', () => {
  test('renders page title', () => {
    render(KV)
    expect(screen.getByText('KV Store')).toBeInTheDocument()
  })

  test('loads entries for default agent', async () => {
    server.use(
      http.get('/api/v1/kv/:agent', () =>
        HttpResponse.json({
          entries: [
            { key: 'user:pref', value: '{"theme":"dark"}', ttl: 3600 },
          ],
        })
      )
    )

    render(KV)
    await waitFor(() => {
      expect(screen.getByText('user:pref')).toBeInTheDocument()
    })
  })

  test('shows empty state when no entries', async () => {
    server.use(
      http.get('/api/v1/kv/:agent', () =>
        HttpResponse.json({ entries: [] })
      )
    )

    render(KV)
    await waitFor(() => {
      expect(screen.getByText(/No keys stored/)).toBeInTheDocument()
    })
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/agents', () =>
        HttpResponse.json({ error: 'KV unavailable' }, { status: 500 })
      )
    )

    render(KV)
    await waitFor(() => {
      expect(screen.getByText('KV unavailable')).toBeInTheDocument()
    })
  })
})
