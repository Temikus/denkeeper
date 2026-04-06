import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Costs from '../../pages/Costs.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Costs page', () => {
  test('renders page title', () => {
    render(Costs)
    expect(screen.getByText('Costs')).toBeInTheDocument()
  })

  test('renders cost data after load', async () => {
    server.use(
      http.get('/api/v1/costs', () =>
        HttpResponse.json({
          global_cost: 2.50,
          max_per_session: 5.0,
          session_count: 3,
          session_costs: {},
          by_agent: [
            { agent: 'default', cost: 2.50, messages: 10, sessions: 3, input_tokens: 50000, output_tokens: 10000 },
          ],
          session_stats: {},
        })
      )
    )

    render(Costs)
    await waitFor(() => {
      expect(screen.getByText('$2.5000')).toBeInTheDocument()
    })
  })

  test('shows loading state', () => {
    server.use(
      http.get('/api/v1/costs', () => new Promise(() => {}))
    )
    render(Costs)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/costs', () =>
        HttpResponse.json({ error: 'Cost data unavailable' }, { status: 500 })
      )
    )

    render(Costs)
    await waitFor(() => {
      expect(screen.getByText('Cost data unavailable')).toBeInTheDocument()
    })
  })
})
