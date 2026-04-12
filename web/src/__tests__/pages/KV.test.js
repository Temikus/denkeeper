import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
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

  test('renders agent selector dropdown', async () => {
    render(KV)
    await waitFor(() => {
      const select = document.querySelector('select')
      expect(select).toBeInTheDocument()
    })
  })

  test('renders prefix filter input with placeholder', async () => {
    render(KV)
    await waitFor(() => {
      // Placeholder is "e.g. cache:"
      const input = screen.getByPlaceholderText('e.g. cache:')
      expect(input).toBeInTheDocument()
    })
  })

  test('clicking entry row expands to show full value', async () => {
    server.use(
      http.get('/api/v1/kv/:agent', () =>
        HttpResponse.json({
          entries: [
            { key: 'test:key', value: '{"expanded":"value_data"}' },
          ],
        })
      )
    )

    render(KV)
    await waitFor(() => screen.getByText('test:key'))

    // Click the row to expand
    const row = screen.getByText('test:key').closest('tr')
    await fireEvent.click(row)

    await waitFor(() => {
      // The expanded section shows "Full value" label
      expect(screen.getByText('Full value')).toBeInTheDocument()
    })
  })

  test('shows Delete button for entries', async () => {
    server.use(
      http.get('/api/v1/kv/:agent', () =>
        HttpResponse.json({
          entries: [
            { key: 'del:key', value: 'v' },
          ],
        })
      )
    )

    render(KV)
    await waitFor(() => {
      expect(screen.getByText('Delete')).toBeInTheDocument()
    })
  })

  test('multiple entries render as rows', async () => {
    server.use(
      http.get('/api/v1/kv/:agent', () =>
        HttpResponse.json({
          entries: [
            { key: 'key1', value: 'val1' },
            { key: 'key2', value: 'val2' },
            { key: 'key3', value: 'val3' },
          ],
        })
      )
    )

    render(KV)
    await waitFor(() => {
      expect(screen.getByText('key1')).toBeInTheDocument()
      expect(screen.getByText('key2')).toBeInTheDocument()
      expect(screen.getByText('key3')).toBeInTheDocument()
    })
  })

  test('shows table column headers', async () => {
    server.use(
      http.get('/api/v1/kv/:agent', () =>
        HttpResponse.json({
          entries: [{ key: 'k', value: 'v' }],
        })
      )
    )

    render(KV)
    await waitFor(() => {
      expect(screen.getByText('Key')).toBeInTheDocument()
      expect(screen.getByText('Value')).toBeInTheDocument()
      expect(screen.getByText('TTL')).toBeInTheDocument()
    })
  })
})
