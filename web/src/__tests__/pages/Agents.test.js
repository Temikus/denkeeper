import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Agents from '../../pages/Agents.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Agents page', () => {
  test('renders agent list', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('default')).toBeInTheDocument()
      expect(screen.getByText('helper')).toBeInTheDocument()
    })
  })

  test('selects first agent by default', async () => {
    render(Agents)
    await waitFor(() => {
      const activeItem = document.querySelector('.active')
      expect(activeItem).toBeInTheDocument()
    })
  })

  test('shows persona sections', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('Persona')).toBeInTheDocument()
    })
  })

  test('shows identity section header', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('# Identity')).toBeInTheDocument()
    })
  })

  test('shows identity section with IDENTITY.md label', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('← IDENTITY.md')).toBeInTheDocument()
    })
  })

  test('shows no agents message when empty', async () => {
    server.use(
      http.get('/api/v1/agents', () => HttpResponse.json([]))
    )

    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('No agents.')).toBeInTheDocument()
    })
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/agents', () =>
        HttpResponse.json({ error: 'Agent load failed' }, { status: 500 })
      )
    )

    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('Agent load failed')).toBeInTheDocument()
    })
  })

  test('shows MODEL stat card', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('MODEL')).toBeInTheDocument()
    })
  })

  test('shows PERMISSION stat card with Autonomous', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('PERMISSION')).toBeInTheDocument()
      expect(screen.getByText('Autonomous')).toBeInTheDocument()
    })
  })

  test('shows permission tier and skill count in agent list', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText(/autonomous.*2 skills/)).toBeInTheDocument()
    })
  })

  test('clicking second agent shows its details', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('helper'))

    await fireEvent.click(screen.getByText('helper'))
    await waitFor(() => {
      expect(screen.getByText('Supervised')).toBeInTheDocument()
    })
  })
})

describe('Agents model config', () => {
  test('clicking MODEL card expands model configuration panel', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))

    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => {
      expect(screen.getByText('Model Configuration')).toBeInTheDocument()
      expect(screen.getByText('Save')).toBeInTheDocument()
      expect(screen.getByText('Cancel')).toBeInTheDocument()
    })
  })

  test('model config panel has ModelSelector component', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))

    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => {
      // ModelSelector renders with a config-label "Model"
      const labels = screen.getAllByText('Model')
      const configLabel = labels.find(l => l.classList.contains('config-label'))
      expect(configLabel).toBeTruthy()
    })
  })

  test('model config panel has description field', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))

    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => {
      expect(screen.getByPlaceholderText('Agent description')).toBeInTheDocument()
    })
  })

  test('Cancel closes config panel', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))

    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => screen.getByText('Model Configuration'))

    await fireEvent.click(screen.getByText('Cancel'))
    await waitFor(() => {
      expect(screen.queryByText('Model Configuration')).not.toBeInTheDocument()
    })
  })

  test('a changed model offers Compare in Evals, with every value encoded', async () => {
    server.use(
      http.get('/api/v1/agents/:name', ({ params }) =>
        HttpResponse.json({ name: params.name, model: 'kimi-k2.6', provider: 'openrouter', permission_tier: 'autonomous' })),
    )
    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))
    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => screen.getByText('Model Configuration'))

    // The saved model is not a comparison.
    expect(screen.queryByTestId('compare-in-evals')).not.toBeInTheDocument()

    const input = document.querySelector('.model-selector input')
    await fireEvent.input(input, { target: { value: 'org/model a&b' } })

    const link = await screen.findByTestId('compare-in-evals')
    const href = link.getAttribute('href')
    expect(href.startsWith('#/evals?')).toBe(true)
    expect(href).not.toContain('&b')
    const q = new URLSearchParams(href.slice('#/evals?'.length))
    expect(q.get('agent')).toBe('default')
    expect(q.get('candidate')).toBe('org/model a&b')
    expect(q.get('provider')).toBe('openrouter')
    expect(screen.getByTestId('compare-hint')).toHaveTextContent('before you switch')

    // An empty field names nothing to compare.
    await fireEvent.input(input, { target: { value: '' } })
    await waitFor(() => expect(screen.queryByTestId('compare-in-evals')).not.toBeInTheDocument())
  })

  test('Save button calls updateAgentConfig API', async () => {
    let patchCalled = false
    server.use(
      http.patch('/api/v1/agents/:name', () => {
        patchCalled = true
        return HttpResponse.json({ ok: true })
      })
    )

    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))

    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => screen.getByText('Model Configuration'))

    // Change description
    const descInput = screen.getByPlaceholderText('Agent description')
    await fireEvent.input(descInput, { target: { value: 'Updated description' } })

    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(patchCalled).toBe(true)
    })
  })
})

describe('Agents permission config', () => {
  test('clicking PERMISSION card expands tier selector', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('PERMISSION'))

    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => {
      expect(screen.getByText('Permission Configuration')).toBeInTheDocument()
      expect(screen.getByLabelText('Permission Tier')).toBeInTheDocument()
    })
  })

  test('permission selector shows three tiers', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('PERMISSION'))

    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => {
      const select = screen.getByLabelText('Permission Tier')
      const options = select.querySelectorAll('option')
      const values = Array.from(options).map(o => o.value)
      expect(values).toContain('autonomous')
      expect(values).toContain('supervised')
      expect(values).toContain('restricted')
    })
  })

  test('changing tier and saving calls API with session_tier', async () => {
    let patchBody = null
    server.use(
      http.patch('/api/v1/agents/:name', async ({ request }) => {
        patchBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )

    render(Agents)
    await waitFor(() => screen.getByText('PERMISSION'))

    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByLabelText('Permission Tier'))

    // Change to restricted
    await fireEvent.change(screen.getByLabelText('Permission Tier'), { target: { value: 'restricted' } })

    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(patchBody).not.toBeNull()
      expect(patchBody.session_tier).toBe('restricted')
    })
  })

  test('supervisor timeout and context inputs appear when supervisor is set', async () => {
    const supervisedAgent = {
      name: 'default', model: 'claude-3-opus', permission_tier: 'supervised',
      supervisor: 'argus', supervisor_timeout: '10s', supervisor_context_messages: 3,
      skill_count: 0, has_tools: false, max_tool_rounds: 50, fallbacks: [],
      persona_sections: {}, adapters: [], tool_names: [],
    }
    server.use(
      http.get('/api/v1/agents/:name', () => HttpResponse.json(supervisedAgent)),
      http.get('/api/v1/agents', () => HttpResponse.json([
        { name: 'default', permission_tier: 'supervised', supervisor: 'argus', skill_count: 0, has_tools: false, fallbacks: [] },
        { name: 'argus', permission_tier: 'autonomous', skill_count: 0, has_tools: false, fallbacks: [] },
      ]))
    )

    let patchBody = null
    server.use(
      http.patch('/api/v1/agents/:name', async ({ request }) => {
        patchBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )

    render(Agents)
    await waitFor(() => screen.getByText('PERMISSION'))

    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByLabelText('Supervisor Timeout'))

    expect(screen.getByLabelText('Supervisor Timeout').value).toBe('10s')
    expect(screen.getByLabelText('Supervisor Context Messages').value).toBe('3')

    await fireEvent.input(screen.getByLabelText('Supervisor Timeout'), { target: { value: '30s' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(patchBody).not.toBeNull()
      expect(patchBody.supervisor_timeout).toBe('30s')
    })
  })

  // Renders the supervised "default" agent with one configured decision model
  // and returns a getter for the body of the next PATCH. listFields extend the
  // agent's list entry; providers replace the provider list.
  function setupDeciderAgent(agentFields = {}, deciders = [{ name: 'jev', provider: 'openrouter', model: 'typesafe/jev-1.13' }],
    listFields = {}, providers = [{ name: 'openrouter', type: 'openrouter', enabled: true, api_key_set: true, serves_decisions: true }]) {
    const agent = {
      name: 'default', model: 'claude-3-opus', permission_tier: 'supervised',
      skill_count: 0, has_tools: false, max_tool_rounds: 50, fallbacks: [],
      persona_sections: {}, adapters: [], tool_names: [], ...agentFields,
    }
    let patchBody = null
    server.use(
      http.get('/api/v1/agents/:name', () => HttpResponse.json(agent)),
      http.get('/api/v1/agents', () => HttpResponse.json([
        { name: 'default', permission_tier: 'supervised', skill_count: 0, has_tools: false, fallbacks: [], ...listFields },
      ])),
      http.get('/api/v1/llm/providers', () => HttpResponse.json({
        default_provider: 'openrouter',
        providers,
        deciders,
      })),
      http.patch('/api/v1/agents/:name', async ({ request }) => {
        patchBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )
    render(Agents)
    return () => patchBody
  }

  test('selecting a decision model in enforce mode sends the decider fields', async () => {
    const patchBody = setupDeciderAgent()
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByLabelText('Decision Model'))

    // Mode and thresholds stay hidden until a decision model is chosen.
    expect(screen.queryByRole('radio', { name: /Enforce/ })).toBeNull()

    await fireEvent.change(screen.getByLabelText('Decision Model'), { target: { value: 'jev' } })
    expect(screen.queryByTestId('decider-enforce-warning')).toBeNull()
    await fireEvent.click(screen.getByRole('radio', { name: /Enforce/ }))
    expect(screen.getByTestId('decider-enforce-warning').textContent).toContain('with no review')
    await fireEvent.input(screen.getByLabelText('Approve at or above'), { target: { value: '0.9' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(patchBody()).not.toBeNull())
    // A decider change always carries the tuning, so the saved config
    // matches the form rather than the API's reset defaults.
    expect(patchBody()).toEqual({
      supervisor_decider: 'jev',
      supervisor_decider_mode: 'enforce',
      supervisor_decider_approve_at: 0.9,
      supervisor_decider_deny_at: 0,
    })
  })

  test('switching decision model resets mode and thresholds in the form', async () => {
    const patchBody = setupDeciderAgent({
      supervisor_decider: 'jev', supervisor_decider_mode: 'enforce',
      supervisor_decider_approve_at: 0.6, supervisor_decider_deny_at: 0.4,
    }, [{ name: 'jev', provider: 'openrouter', model: 'typesafe/jev-1.13' }, { name: 'jev2', provider: 'openrouter', model: 'typesafe/jev-1.14' }])
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByRole('radio', { name: /Enforce/ }))
    expect(screen.getByRole('radio', { name: /Enforce/ }).checked).toBe(true)

    await fireEvent.change(screen.getByLabelText('Decision Model'), { target: { value: 'jev2' } })
    expect(screen.getByRole('radio', { name: /Shadow/ }).checked).toBe(true)
    expect(screen.getByLabelText('Approve at or above').value).toBe('')
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(patchBody()).not.toBeNull())
    expect(patchBody()).toEqual({
      supervisor_decider: 'jev2',
      supervisor_decider_mode: 'shadow',
      supervisor_decider_approve_at: 0,
      supervisor_decider_deny_at: 0,
    })
  })

  test('decision model fields load from the agent and clearing sends an empty name', async () => {
    const patchBody = setupDeciderAgent({
      supervisor_decider: 'jev', supervisor_decider_mode: 'enforce',
      supervisor_decider_approve_at: 0.9, supervisor_decider_deny_at: 0.1,
    })
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByRole('radio', { name: /Enforce/ }))

    expect(screen.getByLabelText('Decision Model').value).toBe('jev')
    expect(screen.getByRole('radio', { name: /Enforce/ }).checked).toBe(true)
    expect(screen.getByLabelText('Approve at or above').value).toBe('0.9')
    expect(screen.getByLabelText('Deny at or below').value).toBe('0.1')

    await fireEvent.change(screen.getByLabelText('Decision Model'), { target: { value: '' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(patchBody()).not.toBeNull())
    expect(patchBody()).toEqual({ supervisor_decider: '' })
  })

  test('clearing a configured threshold sends 0 to restore the default', async () => {
    const patchBody = setupDeciderAgent({
      supervisor_decider: 'jev', supervisor_decider_mode: 'shadow',
      supervisor_decider_approve_at: 0.9, supervisor_decider_deny_at: 0.05,
    })
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByLabelText('Approve at or above'))

    await fireEvent.input(screen.getByLabelText('Approve at or above'), { target: { value: '' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(patchBody()).not.toBeNull())
    expect(patchBody()).toEqual({ supervisor_decider_approve_at: 0 })
  })

  test('out-of-order decision model thresholds show an error and block saving', async () => {
    setupDeciderAgent({ supervisor_decider: 'jev', supervisor_decider_mode: 'shadow' })
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByLabelText('Approve at or above'))

    await fireEvent.input(screen.getByLabelText('Approve at or above'), { target: { value: '0.2' } })
    await fireEvent.input(screen.getByLabelText('Deny at or below'), { target: { value: '0.5' } })

    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('0 < deny threshold < approve threshold < 1'))
    expect(screen.getByText('Save').disabled).toBe(true)
  })

  test('leaving the supervised tier clears a configured decision model', async () => {
    const patchBody = setupDeciderAgent({ supervisor_decider: 'jev', supervisor_decider_mode: 'shadow' })
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByLabelText('Permission Tier'))

    await fireEvent.change(screen.getByLabelText('Permission Tier'), { target: { value: 'autonomous' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(patchBody()).not.toBeNull())
    expect(patchBody()).toEqual({ session_tier: 'autonomous', supervisor_decider: '' })
  })

  test('without decision models the Permission panel links to setting one up', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByLabelText('Permission Tier'))
    await fireEvent.change(screen.getByLabelText('Permission Tier'), { target: { value: 'supervised' } })
    await waitFor(() => screen.getByLabelText('Supervisor Agent'))

    expect(screen.queryByLabelText('Decision Model')).toBeNull()
    expect(screen.getByTestId('decider-empty-link')).toHaveAttribute('href', '#/providers?add=decider&for=default')
  })

  test('without a decision-capable provider the empty state sends you to add OpenRouter', async () => {
    setupDeciderAgent({}, [], {}, [{ name: 'anthropic', type: 'anthropic', enabled: true, serves_decisions: false }])
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))

    await waitFor(() => expect(screen.getByTestId('decider-empty-link')).toHaveAttribute('href', '#/providers?add=openrouter'))
  })

  test('deep link opens Permission with the decision model preselected in shadow, unsaved, and scrolls to the chart', async () => {
    const scrolled = vi.fn()
    Element.prototype.scrollIntoView = scrolled
    window.location.hash = '#/agents/default?card=permission&decider=jev'
    window.dispatchEvent(new HashChangeEvent('hashchange'))
    try {
      const patchBody = setupDeciderAgent({}, [{ name: 'jev', provider: 'openrouter', model: 'typesafe/jev-1.13', used_by: [] }])

      await waitFor(() => expect(screen.getByLabelText('Decision Model')).toHaveValue('jev'))
      expect(screen.getByRole('radio', { name: /Shadow/ })).toBeChecked()
      expect(patchBody()).toBeNull()
      // An unsaved decider has no reviews yet; the panel says how to get some.
      await waitFor(() => expect(screen.getByTestId('calibration-empty').textContent).toContain('Save jev in shadow mode'))
      await waitFor(() => expect(scrolled).toHaveBeenCalled())
    } finally {
      delete Element.prototype.scrollIntoView
      window.location.hash = ''
      window.dispatchEvent(new HashChangeEvent('hashchange'))
    }
  })

  test('using the calibration suggestion fills the threshold input and saves it', async () => {
    server.use(http.get('/api/v1/agents/:name/decider-reviews', () => HttpResponse.json({
      decider: 'jev', reviews: [
        { audit_id: 1, min_score: 0.97, supervisor: 'DENY', supervisor_name: 'argus', time: '2026-10-01T00:00:00Z' },
      ],
    })))
    const patchBody = setupDeciderAgent({ supervisor_decider: 'jev', supervisor_decider_mode: 'shadow' })
    await waitFor(() => screen.getByText('PERMISSION'))
    await fireEvent.click(screen.getByText('PERMISSION'))

    await fireEvent.click(await waitFor(() => screen.getByText('Use 0.98')))
    expect(screen.getByLabelText('Approve at or above')).toHaveValue(0.98)
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(patchBody()).toEqual({ supervisor_decider_approve_at: 0.98 }))
  })

  test('Permission card counts shadow approvals the supervisor did not make', async () => {
    server.use(http.get('/api/v1/agents/:name/decider-reviews', () => HttpResponse.json({
      decider: 'jev', reviews: [
        { audit_id: 1, min_score: 0.97, supervisor: 'DENY', time: '2026-10-01T00:00:00Z' },
        { audit_id: 2, min_score: 0.99, supervisor: 'APPROVE', time: '2026-10-01T00:00:00Z' },
        // An escalation counts too: the decider would have run it unreviewed.
        { audit_id: 3, min_score: 0.96, supervisor: 'ESCALATE', time: '2026-10-01T00:00:00Z' },
        // Below the saved approve threshold: escalates, so not counted.
        { audit_id: 4, min_score: 0.9, supervisor: 'DENY', time: '2026-10-01T00:00:00Z' },
      ],
    })))
    setupDeciderAgent({ supervisor_decider: 'jev', supervisor_decider_mode: 'shadow', supervisor_decider_approve_at: 0.95 })

    await waitFor(() => expect(screen.getByTestId('permission-decider').textContent).toContain('jev · 2 to review'))
  })

  test('Permission card and agent list name the bound decision model', async () => {
    setupDeciderAgent({ supervisor_decider: 'jev', supervisor_decider_mode: 'enforce' }, undefined,
      { supervisor: 'argus', supervisor_decider: 'jev' })

    await waitFor(() => expect(screen.getByTestId('permission-decider')).toHaveTextContent('jev · enforce'))
    expect(screen.getByText(/via argus \+ jev/)).toBeInTheDocument()
  })

  test('changing provider and saving sends llm_provider in PATCH', async () => {
    let patchBody = null
    server.use(
      http.patch('/api/v1/agents/:name', async ({ request }) => {
        patchBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )

    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))

    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => screen.getByLabelText('Provider'))

    // Change provider dropdown
    await fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'ollama' } })

    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(patchBody).not.toBeNull()
      expect(patchBody.llm_provider).toBe('ollama')
    })
  })

  test('model config panel shows Provider dropdown', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))

    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => {
      expect(screen.getByLabelText('Provider')).toBeInTheDocument()
    })
  })

  test('Provider dropdown shows enabled providers', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('MODEL'))

    await fireEvent.click(screen.getByText('MODEL'))
    await waitFor(() => {
      const select = screen.getByLabelText('Provider')
      const options = Array.from(select.querySelectorAll('option'))
      const values = options.map(o => o.value)
      // From the MSW handler: openrouter and ollama are enabled
      expect(values).toContain('openrouter')
      expect(values).toContain('ollama')
    })
  })

  test('clicking same card again collapses it', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('PERMISSION'))

    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => screen.getByText('Permission Configuration'))

    // Click again to collapse
    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => {
      expect(screen.queryByText('Permission Configuration')).not.toBeInTheDocument()
    })
  })
})

describe('Agents persona sections', () => {
  test('shows all four persona section headers', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('# Identity')).toBeInTheDocument()
      expect(screen.getByText('# Soul')).toBeInTheDocument()
      expect(screen.getByText('# User')).toBeInTheDocument()
      expect(screen.getByText('# Memory')).toBeInTheDocument()
    })
  })

  test('shows persona section source labels', async () => {
    render(Agents)
    await waitFor(() => {
      expect(screen.getByText('← IDENTITY.md')).toBeInTheDocument()
      expect(screen.getByText('← SOUL.md')).toBeInTheDocument()
      expect(screen.getByText('← USER.md')).toBeInTheDocument()
      expect(screen.getByText('← MEMORY.md')).toBeInTheDocument()
    })
  })

  test('clicking persona section header expands it', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('# Identity'))

    await fireEvent.click(screen.getByText('# Identity'))
    await waitFor(() => {
      // Expanded section shows persona content loaded from fixture
      // The fixture returns identity content with 'TestBot' in it
      expect(screen.getByText(/TestBot/)).toBeInTheDocument()
    })
  })
})

describe('Agents create', () => {
  test('add button opens inline form', async () => {
    render(Agents)
    await waitFor(() => screen.getByTestId('add-agent-btn'))

    await fireEvent.click(screen.getByTestId('add-agent-btn'))
    await waitFor(() => {
      expect(screen.getByTestId('agent-form')).toBeInTheDocument()
      expect(screen.getByText('Add Agent')).toBeInTheDocument()
    })
  })

  test('create form submits to API', async () => {
    let createCalled = false
    server.use(
      http.post('/api/v1/agents', async ({ request }) => {
        const body = await request.json()
        createCalled = true
        expect(body.name).toBe('new-agent')
        return HttpResponse.json({ name: 'new-agent', status: 'created' }, { status: 201 })
      })
    )

    render(Agents)
    await waitFor(() => screen.getByTestId('add-agent-btn'))

    await fireEvent.click(screen.getByTestId('add-agent-btn'))
    await waitFor(() => screen.getByTestId('agent-name-input'))

    const input = screen.getByTestId('agent-name-input')
    await fireEvent.input(input, { target: { value: 'new-agent' } })
    await fireEvent.click(screen.getByTestId('agent-save-btn'))

    await waitFor(() => expect(createCalled).toBe(true))
  })

  test('form shows validation error for invalid name', async () => {
    render(Agents)
    await waitFor(() => screen.getByTestId('add-agent-btn'))

    await fireEvent.click(screen.getByTestId('add-agent-btn'))
    await waitFor(() => screen.getByTestId('agent-name-input'))

    const input = screen.getByTestId('agent-name-input')
    await fireEvent.input(input, { target: { value: 'INVALID NAME' } })
    await fireEvent.click(screen.getByTestId('agent-save-btn'))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('Agents delete', () => {
  test('delete button hidden for default agent', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('default'))

    // Select default agent (first one).
    await fireEvent.click(screen.getByText('default'))
    await waitFor(() => {
      expect(screen.queryByTestId('delete-agent-btn')).toBeNull()
    })
  })

  test('delete button shows for non-default agent', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('helper'))

    await fireEvent.click(screen.getByText('helper'))
    await waitFor(() => {
      expect(screen.getByTestId('delete-agent-btn')).toBeInTheDocument()
    })
  })

  test('delete button shows confirmation', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('helper'))

    await fireEvent.click(screen.getByText('helper'))
    await waitFor(() => screen.getByTestId('delete-agent-btn'))

    await fireEvent.click(screen.getByTestId('delete-agent-btn'))
    await waitFor(() => {
      expect(screen.getByTestId('delete-confirm')).toBeInTheDocument()
      expect(screen.getByTestId('delete-confirm-btn')).toBeInTheDocument()
    })
  })

  test('confirming delete calls API', async () => {
    let deleteCalled = false
    server.use(
      http.delete('/api/v1/agents/:name', ({ params }) => {
        deleteCalled = true
        expect(params.name).toBe('helper')
        return new HttpResponse(null, { status: 204 })
      })
    )

    render(Agents)
    await waitFor(() => screen.getByText('helper'))

    await fireEvent.click(screen.getByText('helper'))
    await waitFor(() => screen.getByTestId('delete-agent-btn'))

    await fireEvent.click(screen.getByTestId('delete-agent-btn'))
    await waitFor(() => screen.getByTestId('delete-confirm-btn'))

    await fireEvent.click(screen.getByTestId('delete-confirm-btn'))
    await waitFor(() => expect(deleteCalled).toBe(true))
  })

  test('cost limit fields render in permission panel', async () => {
    render(Agents)
    await waitFor(() => screen.getByText('PERMISSION'))

    await fireEvent.click(screen.getByText('PERMISSION'))
    await waitFor(() => {
      expect(screen.getByLabelText('Cost Limit Soft ($)')).toBeInTheDocument()
      expect(screen.getByLabelText('Cost Limit Hard ($)')).toBeInTheDocument()
    })
  })
})
