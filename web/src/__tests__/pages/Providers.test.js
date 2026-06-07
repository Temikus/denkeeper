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

  test('save provider triggers PATCH and shows restart note', async () => {
    server.use(
      http.patch('/api/v1/llm/providers/:name', () => HttpResponse.json({ ok: true }))
    )

    render(Providers)
    await waitFor(() => {
      expect(screen.getByText('Anthropic')).toBeInTheDocument()
    })

    const editButtons = screen.getAllByText('Edit')
    await fireEvent.click(editButtons[1])

    await waitFor(() => {
      expect(screen.getByText('Save')).toBeInTheDocument()
    })

    // Enter an API key so the patch has content
    const apiKeyInput = screen.getByLabelText('API Key')
    await fireEvent.input(apiKeyInput, { target: { value: 'sk-test-123' } })

    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(screen.getByText('Saved — restart to apply')).toBeInTheDocument()
    })
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

  test('add form shows auth toggle only for anthropic', async () => {
    render(Providers)
    await waitFor(() => expect(screen.getByTestId('add-provider-btn')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await waitFor(() => expect(screen.getByTestId('provider-type-select')).toBeInTheDocument())

    // Default type is openai — no auth toggle.
    expect(screen.queryByText('Claude subscription (OAuth)')).not.toBeInTheDocument()

    // Switch to anthropic — toggle appears.
    await fireEvent.change(screen.getByTestId('provider-type-select'), { target: { value: 'anthropic' } })
    await waitFor(() => {
      expect(screen.getByText('Claude subscription (OAuth)')).toBeInTheDocument()
    })
  })

  test('selecting OAuth reveals token field and billing banner, hides API key', async () => {
    render(Providers)
    await waitFor(() => expect(screen.getByTestId('add-provider-btn')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await waitFor(() => expect(screen.getByTestId('provider-type-select')).toBeInTheDocument())

    await fireEvent.change(screen.getByTestId('provider-type-select'), { target: { value: 'anthropic' } })
    const oauthRadio = await screen.findByDisplayValue('oauth')
    await fireEvent.click(oauthRadio)

    await waitFor(() => {
      expect(screen.getByTestId('provider-oauth-input')).toBeInTheDocument()
      expect(screen.getByText(/Subscription billing/)).toBeInTheDocument()
      expect(screen.getByText(/claude setup-token/)).toBeInTheDocument()
      // API key field hidden in OAuth mode.
      expect(screen.queryByLabelText('API Key')).not.toBeInTheDocument()
    })
  })

  test('create OAuth provider posts auth and oauth_token', async () => {
    let postBody = null
    server.use(
      http.post('/api/v1/llm/providers', async ({ request }) => {
        postBody = await request.json()
        return HttpResponse.json({ name: 'claude-sub', status: 'created' }, { status: 201 })
      })
    )
    render(Providers)
    await waitFor(() => expect(screen.getByTestId('add-provider-btn')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await waitFor(() => expect(screen.getByTestId('provider-type-select')).toBeInTheDocument())

    await fireEvent.input(screen.getByTestId('provider-name-input'), { target: { value: 'claude-sub' } })
    await fireEvent.change(screen.getByTestId('provider-type-select'), { target: { value: 'anthropic' } })
    const oauthRadio = await screen.findByDisplayValue('oauth')
    await fireEvent.click(oauthRadio)
    await fireEvent.input(screen.getByTestId('provider-oauth-input'), { target: { value: 'sk-ant-oat01-xyz' } })
    await fireEvent.click(screen.getByTestId('provider-save-btn'))

    await waitFor(() => {
      expect(postBody).not.toBeNull()
      expect(postBody.auth).toBe('oauth')
      expect(postBody.oauth_token).toBe('sk-ant-oat01-xyz')
      expect(postBody.api_key).toBeUndefined()
    })
  })

  test('create OAuth provider blocks when token missing', async () => {
    render(Providers)
    await waitFor(() => expect(screen.getByTestId('add-provider-btn')).toBeInTheDocument())
    await fireEvent.click(screen.getByTestId('add-provider-btn'))
    await waitFor(() => expect(screen.getByTestId('provider-type-select')).toBeInTheDocument())

    await fireEvent.input(screen.getByTestId('provider-name-input'), { target: { value: 'claude-sub' } })
    await fireEvent.change(screen.getByTestId('provider-type-select'), { target: { value: 'anthropic' } })
    const oauthRadio = await screen.findByDisplayValue('oauth')
    await fireEvent.click(oauthRadio)
    await fireEvent.click(screen.getByTestId('provider-save-btn'))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Paste a subscription token')
    })
  })

  test('test connection button reports success', async () => {
    render(Providers)
    await waitFor(() => expect(screen.getByText('Anthropic')).toBeInTheDocument())

    const testButtons = screen.getAllByTestId('test-provider-btn')
    await fireEvent.click(testButtons[0])

    await waitFor(() => {
      expect(screen.getByText(/Connected — 5 models available/)).toBeInTheDocument()
    })
  })

  test('test connection button reports failure', async () => {
    server.use(
      http.post('/api/v1/llm/providers/:name/test', () =>
        HttpResponse.json({ ok: false, error: 'invalid token' }, { status: 502 })
      )
    )
    render(Providers)
    await waitFor(() => expect(screen.getByText('Anthropic')).toBeInTheDocument())

    const testButtons = screen.getAllByTestId('test-provider-btn')
    await fireEvent.click(testButtons[0])

    await waitFor(() => {
      expect(screen.getByText(/invalid token/)).toBeInTheDocument()
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
