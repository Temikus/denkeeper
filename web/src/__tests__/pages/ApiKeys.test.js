import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import ApiKeys from '../../pages/ApiKeys.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('ApiKeys page', () => {
  test('renders page title', async () => {
    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText('API Keys')).toBeInTheDocument()
    })
  })

  test('renders key table with data', async () => {
    render(ApiKeys)
    await waitFor(() => {
      // The fixture key name is 'test-key'
      expect(screen.getByText('test-key')).toBeInTheDocument()
    })
  })

  test('shows empty state when no keys', async () => {
    server.use(
      http.get('/api/v1/keys', () => HttpResponse.json([]))
    )

    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText(/No API keys/)).toBeInTheDocument()
    })
  })

  test('error state shows error', async () => {
    server.use(
      http.get('/api/v1/keys', () =>
        HttpResponse.json({ error: 'Key store error' }, { status: 500 })
      )
    )

    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText('Key store error')).toBeInTheDocument()
    })
  })
})
