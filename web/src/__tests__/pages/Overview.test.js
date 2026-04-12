import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'

// Mock router to capture navigate calls
const mockNavigate = vi.fn()
vi.mock('../../router.js', async () => {
  const { writable } = await import('svelte/store')
  return {
    navigate: (...args) => mockNavigate(...args),
    currentRoute: writable('overview'),
  }
})

const Overview = (await import('../../pages/Overview.svelte')).default

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  mockNavigate.mockClear()
})

describe('Overview page', () => {
  test('shows loading state initially', () => {
    server.use(
      http.get('/api/v1/health', () => new Promise(() => {})),
    )
    render(Overview)
    expect(screen.getByText('Loading…')).toBeInTheDocument()
  })

  test('renders dashboard cards after data loads', async () => {
    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Status')).toBeInTheDocument()
      expect(screen.getByText('ok')).toBeInTheDocument()
    })
    // "Agents" appears both as card label and section title — use getAllByText
    expect(screen.getAllByText('Agents').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('Total Cost')).toBeInTheDocument()
    expect(screen.getByText('$1.2500')).toBeInTheDocument()
    expect(screen.getByText('Session Budget')).toBeInTheDocument()
    expect(screen.getByText('$5.0000')).toBeInTheDocument()
    expect(screen.getByText('Active Sessions')).toBeInTheDocument()
  })

  test('pending approvals card shows count with warn style', async () => {
    server.use(
      http.get('/api/v1/approvals', () =>
        HttpResponse.json([
          { id: 'a1', status: 'pending' },
          { id: 'a2', status: 'pending' },
        ])
      ),
    )

    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Pending Approvals')).toBeInTheDocument()
    })

    // The value inside the Pending Approvals card should have the warn class
    const pendingCard = screen.getByText('Pending Approvals').closest('.card')
    await waitFor(() => {
      const value = pendingCard.querySelector('.value')
      expect(value.classList.contains('warn')).toBe(true)
    })
  })

  test('clicking Pending Approvals card navigates to approvals', async () => {
    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Pending Approvals')).toBeInTheDocument()
    })

    const card = screen.getByText('Pending Approvals').closest('.card')
    await fireEvent.click(card)
    expect(mockNavigate).toHaveBeenCalledWith('approvals')
  })

  test('clicking Total Cost card navigates to costs', async () => {
    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Total Cost')).toBeInTheDocument()
    })

    const card = screen.getByText('Total Cost').closest('.card')
    await fireEvent.click(card)
    expect(mockNavigate).toHaveBeenCalledWith('costs')
  })

  test('cost breakdown table renders session costs', async () => {
    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Cost Breakdown')).toBeInTheDocument()
    })

    expect(screen.getByText('sess-1')).toBeInTheDocument()
    expect(screen.getByText('sess-2')).toBeInTheDocument()
    expect(screen.getByText('$0.750000')).toBeInTheDocument()
    expect(screen.getByText('$0.500000')).toBeInTheDocument()
  })

  test('sort toggles on column header click', async () => {
    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Cost Breakdown')).toBeInTheDocument()
    })

    // Default sort is by cost descending — sess-1 (0.75) first
    const rows = document.querySelectorAll('.cost-table tbody tr')
    expect(rows[0].textContent).toContain('sess-1')
    expect(rows[1].textContent).toContain('sess-2')

    // Click Session ID header to sort by id ascending
    await fireEvent.click(screen.getByText('Session ID'))
    await waitFor(() => {
      const sortedRows = document.querySelectorAll('.cost-table tbody tr')
      expect(sortedRows[0].textContent).toContain('sess-1')
    })
  })

  test('agent cards show agent details', async () => {
    render(Overview)

    await waitFor(() => {
      const agentCards = document.querySelectorAll('.agent-card')
      expect(agentCards.length).toBe(2)
    })

    expect(screen.getByText('default')).toBeInTheDocument()
    expect(screen.getByText('helper')).toBeInTheDocument()
    expect(screen.getByText('autonomous')).toBeInTheDocument()
    expect(screen.getByText('supervised')).toBeInTheDocument()
    expect(screen.getByText('claude-3-opus')).toBeInTheDocument()
    expect(screen.getByText('gpt-4o')).toBeInTheDocument()
  })

  test('clicking agent card navigates to agents page', async () => {
    render(Overview)
    await waitFor(() => {
      const agentCards = document.querySelectorAll('.agent-card')
      expect(agentCards.length).toBe(2)
    })

    const agentCard = document.querySelector('.agent-card')
    await fireEvent.click(agentCard)
    expect(mockNavigate).toHaveBeenCalledWith('agents')
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      // agents() uses apiFetch which throws on non-ok status
      http.get('/api/v1/agents', () =>
        HttpResponse.json({ error: 'Failed to load agents' }, { status: 500 })
      ),
    )

    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Failed to load agents')).toBeInTheDocument()
    })
  })

  test('onboarding card renders when show_onboarding is true', async () => {
    server.use(
      http.get('/api/v1/onboarding', () => HttpResponse.json({
        show_onboarding: true,
        steps: [
          { id: 'auth', label: 'Set up authentication', done: true },
          { id: 'agent', label: 'Configure an agent', done: false },
          { id: 'adapter', label: 'Connect a chat adapter', done: false },
          { id: 'provider', label: 'Add an LLM provider', done: true },
          { id: 'skill', label: 'Create a skill file', done: false },
        ],
        dismissed: false,
      })),
    )

    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Setup Checklist')).toBeInTheDocument()
    })

    // Completed steps show checkmark, incomplete steps are links
    expect(screen.getByText('Configure an agent').closest('a')).toBeTruthy()
    expect(screen.queryByText('Welcome to Denkeeper!')).not.toBeInTheDocument()
  })

  test('welcome banner shows when all steps incomplete', async () => {
    server.use(
      http.get('/api/v1/onboarding', () => HttpResponse.json({
        show_onboarding: true,
        steps: [
          { id: 'auth', label: 'Set up authentication', done: false },
          { id: 'agent', label: 'Configure an agent', done: false },
          { id: 'adapter', label: 'Connect a chat adapter', done: false },
          { id: 'provider', label: 'Add an LLM provider', done: false },
          { id: 'skill', label: 'Create a skill file', done: false },
        ],
        dismissed: false,
      })),
    )

    render(Overview)
    await waitFor(() => {
      expect(screen.getByText(/Welcome to Denkeeper/)).toBeInTheDocument()
    })
  })

  test('onboarding card not shown when dismissed', async () => {
    server.use(
      http.get('/api/v1/onboarding', () => HttpResponse.json({
        show_onboarding: false,
        steps: [],
        dismissed: true,
      })),
    )

    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Status')).toBeInTheDocument()
    })
    expect(screen.queryByText('Setup Checklist')).not.toBeInTheDocument()
  })

  test('dismiss button hides onboarding card', async () => {
    server.use(
      http.get('/api/v1/onboarding', () => HttpResponse.json({
        show_onboarding: true,
        steps: [
          { id: 'auth', label: 'Set up authentication', done: false },
        ],
        dismissed: false,
      })),
    )

    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Setup Checklist')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Dismiss'))
    await waitFor(() => {
      expect(screen.queryByText('Setup Checklist')).not.toBeInTheDocument()
    })
  })

  test('no cost breakdown section when session_costs is empty', async () => {
    server.use(
      http.get('/api/v1/costs', () =>
        HttpResponse.json({
          global_cost: 0,
          max_per_session: 5.0,
          session_count: 0,
          session_costs: {},
          agents: {},
        })
      ),
    )

    render(Overview)
    await waitFor(() => {
      expect(screen.getByText('Status')).toBeInTheDocument()
    })

    expect(screen.queryByText('Cost Breakdown')).not.toBeInTheDocument()
  })
})
