import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Costs from '../../pages/Costs.svelte'

const costsResponse = {
  global_cost: 2.50,
  cost_limits: { soft: 5.0, hard: 10.0 },
  max_per_session: 5.0,
  session_count: 3,
  by_agent: [
    { agent: 'default', cost: 2.00, messages: 10, sessions: 3, input_tokens: 50000, output_tokens: 10000 },
    { agent: 'helper', cost: 0.50, messages: 5, sessions: 1, input_tokens: 20000, output_tokens: 5000 },
  ],
  session_stats: {
    'default:sess-1': { id: 'default:sess-1', cost: 1.50, messages: 6, input_tokens: 30000, output_tokens: 6000 },
    'default:sess-2': { id: 'default:sess-2', cost: 0.50, messages: 4, input_tokens: 20000, output_tokens: 4000 },
  },
}

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  server.use(
    http.get('/api/v1/costs', () => HttpResponse.json(costsResponse))
  )
})

describe('Costs page', () => {
  test('renders page title', () => {
    render(Costs)
    expect(screen.getByText('Costs')).toBeInTheDocument()
  })

  test('renders total cost', async () => {
    render(Costs)
    await waitFor(() => {
      expect(screen.getByText('$2.5000')).toBeInTheDocument()
    })
  })

  test('renders agent cost rows', async () => {
    render(Costs)
    await waitFor(() => {
      // Agent names in the table
      expect(screen.getByText('default')).toBeInTheDocument()
      expect(screen.getByText('helper')).toBeInTheDocument()
    })
  })

  test('renders cost limits', async () => {
    render(Costs)
    await waitFor(() => {
      expect(screen.getByText('Soft Limit')).toBeInTheDocument()
      expect(screen.getByText('Hard Limit')).toBeInTheDocument()
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

  test('renders sortable column headers', async () => {
    render(Costs)
    await waitFor(() => {
      const headers = screen.getAllByRole('columnheader')
      const texts = headers.map(h => h.textContent.trim())
      expect(texts.some(t => t.startsWith('Agent'))).toBe(true)
      expect(texts.some(t => t.startsWith('Cost'))).toBe(true)
      expect(texts.some(t => t.startsWith('Messages'))).toBe(true)
      expect(texts.some(t => t.startsWith('Sessions'))).toBe(true)
    })
  })

  test('clicking agent row toggles expansion', async () => {
    render(Costs)
    await waitFor(() => screen.getByText('default'))

    // Click to expand
    const agentRow = screen.getByText('default').closest('tr')
    await fireEvent.click(agentRow)

    // Should show session breakdown — session costs use toFixed(6)
    await waitFor(() => {
      expect(screen.getByText('$1.500000')).toBeInTheDocument()
    })

    // Click again to collapse
    await fireEvent.click(agentRow)
    await waitFor(() => {
      expect(screen.queryByText('$1.500000')).not.toBeInTheDocument()
    })
  })

  test('shows token counts in summary', async () => {
    render(Costs)
    await waitFor(() => {
      // 50000 + 20000 = 70k total input tokens
      expect(screen.getByText('70.0k')).toBeInTheDocument()
    })
  })

  test('shows session count in summary card', async () => {
    render(Costs)
    await waitFor(() => {
      const matches = screen.getAllByText('Sessions')
      expect(matches.length).toBeGreaterThanOrEqual(1)
    })
  })
})
