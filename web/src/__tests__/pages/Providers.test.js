import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Providers from '../../pages/Providers.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Providers page', () => {
  test('renders page title', async () => {
    render(Providers)
    expect(screen.getByText('Providers')).toBeInTheDocument()
  })

  test('renders LLM Defaults section with data', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('LLM Defaults')).toBeInTheDocument()
      expect(screen.getByText('openrouter')).toBeInTheDocument()
      expect(screen.getByText('anthropic/claude-3-opus')).toBeInTheDocument()
    })
  })

  test('renders all four provider cards', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('Anthropic')).toBeInTheDocument()
      expect(screen.getByText('OpenRouter')).toBeInTheDocument()
      expect(screen.getByText('OpenAI')).toBeInTheDocument()
      expect(screen.getByText('Ollama')).toBeInTheDocument()
    })
  })

  test('shows enabled/not-configured status per provider', async () => {
    render(Providers)
    await waitFor(() => {
      const statuses = document.querySelectorAll('.provider-status')
      expect(statuses).toHaveLength(4)
      // anthropic and openai are not configured; openrouter and ollama are enabled
      const texts = [...statuses].map(s => s.textContent.trim())
      expect(texts).toEqual(['Not configured', 'Enabled', 'Not configured', 'Enabled'])
    })
  })

  test('shows API key status and base URL fields', async () => {
    render(Providers)
    await waitFor(() => {
      // Anthropic, OpenRouter, OpenAI show API key status (not Ollama)
      const keyLabels = screen.getAllByText('API Key')
      expect(keyLabels).toHaveLength(3)

      // Ollama shows its base URL
      expect(screen.getByText('http://localhost:11434')).toBeInTheDocument()
    })
  })

  test('shows loading state initially', () => {
    render(Providers)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/llm/providers', () =>
        HttpResponse.json({ error: 'Provider fetch failed' }, { status: 500 })
      )
    )

    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('Provider fetch failed')).toBeInTheDocument()
    })
  })

  test('edit config button shows form with current values', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('LLM Defaults')).toBeInTheDocument()
    })

    // Click Edit on the config card
    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[0])

    await waitFor(() => {
      expect(screen.getByLabelText('Default Provider')).toBeInTheDocument()
    })
  })

  test('save config triggers PATCH and shows success', async () => {
    server.use(
      http.patch('/api/v1/llm/config', () => HttpResponse.json({ ok: true }))
    )

    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('LLM Defaults')).toBeInTheDocument()
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[0])

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(screen.getByText('Saved')).toBeInTheDocument()
    })
  })

  test('cancel config edit returns to display mode', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('LLM Defaults')).toBeInTheDocument()
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[0])

    await waitFor(() => {
      expect(screen.getByText('Cancel')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Cancel'))

    await waitFor(() => {
      // Should be back in display mode with Edit button
      const edits = screen.getAllByText('Edit')
      expect(edits.length).toBeGreaterThan(0)
    })
  })

  test('edit provider shows form fields', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('Anthropic')).toBeInTheDocument()
    })

    // Click Edit on Anthropic card (second Edit button — first is config)
    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[1])

    await waitFor(() => {
      expect(screen.getByLabelText('API Key')).toBeInTheDocument()
      expect(screen.getByLabelText('Base URL')).toBeInTheDocument()
    })
  })

  // Opens the second provider's edit form (openrouter) and returns after Save shows.
  async function editSecondProvider() {
    render(Providers)
    await waitFor(() => expect(screen.getByText('Anthropic')).toBeInTheDocument())
    await fireEvent.click(screen.getAllByText('Edit')[1])
    await waitFor(() => expect(screen.getByText('Save')).toBeInTheDocument())
  }

  test('a live save says Saved with no restart hint', async () => {
    server.use(http.patch('/api/v1/llm/providers/:name', () => HttpResponse.json({ status: 'updated', restart_required: false })))
    await editSecondProvider()

    await fireEvent.input(screen.getByLabelText('API Key'), { target: { value: 'sk-test-123' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(screen.getByText('Saved')).toBeInTheDocument())
  })

  test('a save the server could not apply live asks for a restart', async () => {
    server.use(http.patch('/api/v1/llm/providers/:name', () => HttpResponse.json({ status: 'updated', restart_required: true })))
    await editSecondProvider()

    await fireEvent.input(screen.getByLabelText('API Key'), { target: { value: 'sk-test-123' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(screen.getByText('Saved. Restart to apply.')).toBeInTheDocument())
  })

  test('a changed price override asks for a restart', async () => {
    server.use(http.patch('/api/v1/llm/providers/:name', () => HttpResponse.json({ status: 'updated', restart_required: false })))
    await editSecondProvider()

    await fireEvent.input(screen.getByLabelText('Fallback Rate ($/1K tokens)'), { target: { value: '0.002' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => expect(screen.getByText('Saved. Restart to apply price overrides.')).toBeInTheDocument())
  })

  test('creating a provider without a live runtime says to restart', async () => {
    server.use(http.post('/api/v1/llm/providers', () => HttpResponse.json({ name: 'my-openai', status: 'created', restart_required: true }, { status: 201 })))
    render(Providers)
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await fireEvent.input(screen.getByTestId('provider-name-input'), { target: { value: 'my-openai' } })
    await fireEvent.click(screen.getByTestId('provider-save-btn'))

    await waitFor(() => expect(screen.getByTestId('provider-create-notice')).toHaveTextContent('my-openai is saved. Restart denkeeper to use it.'))
  })

  test('deleting the provider a restart notice names clears the notice', async () => {
    server.use(http.post('/api/v1/llm/providers', () => HttpResponse.json({ name: 'openai', status: 'created', restart_required: true }, { status: 201 })))
    render(Providers)
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await fireEvent.input(screen.getByTestId('provider-name-input'), { target: { value: 'openai' } })
    await fireEvent.click(screen.getByTestId('provider-save-btn'))
    await waitFor(() => expect(screen.getByTestId('provider-create-notice')).toBeInTheDocument())

    await fireEvent.click(screen.getAllByTestId('delete-provider-btn')[2])
    await fireEvent.click(screen.getByTestId('delete-confirm-btn'))

    await waitFor(() => expect(screen.queryByTestId('provider-create-notice')).not.toBeInTheDocument())
  })

  test('creating a provider that applies live shows no restart notice', async () => {
    server.use(http.post('/api/v1/llm/providers', () => HttpResponse.json({ name: 'my-openai', status: 'created', restart_required: false }, { status: 201 })))
    render(Providers)
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await fireEvent.input(screen.getByTestId('provider-name-input'), { target: { value: 'my-openai' } })
    await fireEvent.click(screen.getByTestId('provider-save-btn'))

    await waitFor(() => expect(screen.queryByTestId('provider-form')).not.toBeInTheDocument())
    expect(screen.queryByTestId('provider-create-notice')).not.toBeInTheDocument()
  })

  test('renders Add Provider button', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByTestId('add-provider-btn')).toBeInTheDocument()
    })
  })

  test('clicking Add Provider shows inline form', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByTestId('add-provider-btn')).toBeInTheDocument()
    })
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await waitFor(() => {
      expect(screen.getByTestId('provider-form')).toBeInTheDocument()
      expect(screen.getByTestId('provider-name-input')).toBeInTheDocument()
      expect(screen.getByTestId('provider-type-select')).toBeInTheDocument()
    })
  })

  test('create provider submits POST and closes form', async () => {
    let postCalled = false
    server.use(
      http.post('/api/v1/llm/providers', () => {
        postCalled = true
        return HttpResponse.json({ name: 'my-openai', status: 'created' }, { status: 201 })
      })
    )
    render(Providers)
    await waitFor(() => expect(screen.getByTestId('add-provider-btn')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await waitFor(() => expect(screen.getByTestId('provider-name-input')).toBeInTheDocument())

    await fireEvent.input(screen.getByTestId('provider-name-input'), { target: { value: 'my-openai' } })
    await fireEvent.click(screen.getByTestId('provider-save-btn'))

    await waitFor(() => {
      expect(postCalled).toBe(true)
      expect(screen.queryByTestId('provider-form')).not.toBeInTheDocument()
    })
  })

  test('create provider shows error for invalid name', async () => {
    render(Providers)
    await waitFor(() => expect(screen.getByTestId('add-provider-btn')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await waitFor(() => expect(screen.getByTestId('provider-name-input')).toBeInTheDocument())

    await fireEvent.input(screen.getByTestId('provider-name-input'), { target: { value: 'INVALID' } })
    await fireEvent.click(screen.getByTestId('provider-save-btn'))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('lowercase alphanumeric')
    })
  })

  test('delete button shows confirmation panel', async () => {
    render(Providers)
    await waitFor(() => expect(screen.getByText('Anthropic')).toBeInTheDocument())

    const deleteButtons = screen.getAllByTestId('delete-provider-btn')
    await fireEvent.click(deleteButtons[0])

    await waitFor(() => {
      expect(screen.getByTestId('delete-confirm')).toBeInTheDocument()
    })
  })

  test('provider card shows cost fields in display mode', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('Anthropic')).toBeInTheDocument()
      // Anthropic has cost_limit_soft: 5.0 and cost_limit_hard: 10.0
      expect(screen.getByText('$5.00')).toBeInTheDocument()
      expect(screen.getByText('$10.00')).toBeInTheDocument()
    })
  })

  test('edit provider shows cost inputs', async () => {
    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('Anthropic')).toBeInTheDocument()
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[1])

    await waitFor(() => {
      expect(screen.getByLabelText('Soft Limit ($)')).toBeInTheDocument()
      expect(screen.getByLabelText('Hard Limit ($)')).toBeInTheDocument()
      expect(screen.getByLabelText('Fallback Rate ($/1K tokens)')).toBeInTheDocument()
      expect(screen.getByText('Model Price Overrides')).toBeInTheDocument()
    })
  })

  test('save provider sends cost fields in PATCH', async () => {
    let patchBody = null
    server.use(
      http.patch('/api/v1/llm/providers/:name', async ({ request }) => {
        patchBody = await request.json()
        return HttpResponse.json({ status: 'updated' })
      })
    )

    render(Providers)
    await waitFor(() => expect(screen.getByText('Anthropic')).toBeInTheDocument())

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[1])

    await waitFor(() => expect(screen.getByLabelText('Soft Limit ($)')).toBeInTheDocument())

    const softInput = screen.getByLabelText('Soft Limit ($)')
    await fireEvent.input(softInput, { target: { value: '3' } })

    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(patchBody).not.toBeNull()
      expect(patchBody.cost_limit_soft).toBe(3)
    })
  })

  test('confirm delete calls DELETE and refreshes list', async () => {
    let deleteCalled = false
    server.use(
      http.delete('/api/v1/llm/providers/:name', () => {
        deleteCalled = true
        return new HttpResponse(null, { status: 204 })
      })
    )
    render(Providers)
    await waitFor(() => expect(screen.getByText('Anthropic')).toBeInTheDocument())

    const deleteButtons = screen.getAllByTestId('delete-provider-btn')
    await fireEvent.click(deleteButtons[0])

    await waitFor(() => expect(screen.getByTestId('delete-confirm-btn')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('delete-confirm-btn'))

    await waitFor(() => {
      expect(deleteCalled).toBe(true)
    })
  })
})
