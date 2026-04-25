import { describe, test, expect, beforeEach } from 'vitest'
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
