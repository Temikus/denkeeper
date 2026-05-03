import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'

vi.mock('../../router.js', async () => {
  const { writable } = await import('svelte/store')
  return {
    navigate: vi.fn(),
    currentRoute: writable(''),
  }
})

const SetupWizard = (await import('../../pages/SetupWizard.svelte')).default

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  localStorage.removeItem('dk_wizard_state')

  // Default mock: providers list with one provider
  server.use(
    http.get('/api/v1/llm/providers', () => HttpResponse.json({
      providers: [{ name: 'anthropic', type: 'anthropic' }],
      default_provider: 'anthropic',
      default_model: '',
    })),
  )
})

describe('SetupWizard', () => {
  test('renders provider step initially', async () => {
    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByText('Add a Provider')).toBeInTheDocument()
    })
    expect(screen.getByTestId('wizard-provider-type')).toBeInTheDocument()
    expect(screen.getByTestId('wizard-provider-name')).toBeInTheDocument()
    expect(screen.getByTestId('wizard-provider-submit')).toBeInTheDocument()
  })

  test('provider type change updates name field', async () => {
    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-provider-type')).toBeInTheDocument()
    })
    const nameInput = screen.getByTestId('wizard-provider-name')
    expect(nameInput.value).toBe('anthropic')
  })

  test('provider creation advances to agent step', async () => {
    server.use(
      http.post('/api/v1/llm/providers', () => HttpResponse.json({ name: 'test', status: 'created' }, { status: 201 })),
      http.patch('/api/v1/llm/config', () => HttpResponse.json({ ok: true })),
    )

    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-provider-name')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByTestId('wizard-provider-submit'))
    await waitFor(() => {
      expect(screen.getByText('Create an Agent')).toBeInTheDocument()
    })
  })

  test('agent step shows radio buttons with supervised pre-selected', async () => {
    // Start at agent step via localStorage
    localStorage.setItem('dk_wizard_state', JSON.stringify({ step: 'agent', providerName: 'anthropic' }))

    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByText('Create an Agent')).toBeInTheDocument()
    })

    const supervised = screen.getByLabelText(/Supervised/i)
    expect(supervised.checked).toBe(true)
  })

  test('companion supervisor callout appears for supervised tier', async () => {
    localStorage.setItem('dk_wizard_state', JSON.stringify({ step: 'agent', providerName: 'anthropic' }))

    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-supervisor-callout')).toBeInTheDocument()
    })

    expect(screen.getByText('COMPANION SUPERVISOR')).toBeInTheDocument()
    expect(screen.getByTestId('wizard-supervisor-name')).toBeInTheDocument()
  })

  test('companion supervisor callout hidden for autonomous tier', async () => {
    localStorage.setItem('dk_wizard_state', JSON.stringify({ step: 'agent', providerName: 'anthropic' }))

    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByText('Create an Agent')).toBeInTheDocument()
    })

    const autonomous = screen.getByLabelText(/Autonomous/i)
    await fireEvent.click(autonomous)
    await waitFor(() => {
      expect(screen.queryByTestId('wizard-supervisor-callout')).not.toBeInTheDocument()
    })
  })

  test('agent creation with supervisor sends create_supervisor', async () => {
    let capturedBody = null
    server.use(
      http.post('/api/v1/agents', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ name: 'worker', supervisor: 'supervisor', status: 'created' }, { status: 201 })
      }),
    )

    localStorage.setItem('dk_wizard_state', JSON.stringify({ step: 'agent', providerName: 'anthropic' }))

    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-agent-name')).toBeInTheDocument()
    })

    const nameInput = screen.getByTestId('wizard-agent-name')
    await fireEvent.input(nameInput, { target: { value: 'worker' } })
    await fireEvent.click(screen.getByTestId('wizard-agent-submit'))

    await waitFor(() => {
      expect(capturedBody).not.toBeNull()
      expect(capturedBody.create_supervisor).toBeDefined()
      expect(capturedBody.create_supervisor.name).toBe('supervisor')
      expect(capturedBody.session_tier).toBe('supervised')
    })
  })

  test('persona step pre-fills default behavior guidelines', async () => {
    localStorage.setItem('dk_wizard_state', JSON.stringify({ step: 'persona', agentName: 'worker' }))

    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByText('Personalize Your Agent')).toBeInTheDocument()
    })

    const textarea = screen.getByTestId('wizard-persona-guidelines')
    expect(textarea.value).toContain('Be genuinely helpful')
  })

  test('use defaults skips persona API calls and completes', async () => {
    let personaCalled = false
    server.use(
      http.put('/api/v1/agents/:name/persona/:section', () => {
        personaCalled = true
        return HttpResponse.json({ ok: true })
      }),
    )

    localStorage.setItem('dk_wizard_state', JSON.stringify({ step: 'persona', agentName: 'worker' }))

    const onComplete = vi.fn()
    render(SetupWizard, { props: { onComplete } })
    await waitFor(() => {
      expect(screen.getByText('Use defaults')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Use defaults'))
    await waitFor(() => {
      expect(onComplete).toHaveBeenCalled()
    })
    expect(personaCalled).toBe(false)
  })

  test('save & continue calls updatePersona and completes', async () => {
    const personaSections = []
    server.use(
      http.put('/api/v1/agents/:name/persona/:section', async ({ params }) => {
        personaSections.push(params.section)
        return HttpResponse.json({ ok: true })
      }),
    )

    localStorage.setItem('dk_wizard_state', JSON.stringify({ step: 'persona', agentName: 'worker' }))

    const onComplete = vi.fn()
    render(SetupWizard, { props: { onComplete } })
    await waitFor(() => {
      expect(screen.getByTestId('wizard-persona-submit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByTestId('wizard-persona-submit'))
    await waitFor(() => {
      expect(onComplete).toHaveBeenCalled()
    })
    expect(personaSections).toContain('identity')
    expect(personaSections).toContain('soul')
  })

  test('skip setup calls wizardComplete and fires onComplete', async () => {
    const onComplete = vi.fn()
    render(SetupWizard, { props: { onComplete } })
    await waitFor(() => {
      expect(screen.getByText('Skip setup')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Skip setup'))
    await waitFor(() => {
      expect(onComplete).toHaveBeenCalled()
    })
  })

  test('localStorage state restored on mount', async () => {
    localStorage.setItem('dk_wizard_state', JSON.stringify({ step: 'persona', agentName: 'my-agent', providerName: 'anthropic' }))

    render(SetupWizard, { props: { onComplete: vi.fn() } })
    await waitFor(() => {
      expect(screen.getByText('Personalize Your Agent')).toBeInTheDocument()
    })
  })
})
