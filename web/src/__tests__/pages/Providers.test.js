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
      expect(screen.getByText('$0.50')).toBeInTheDocument()
      expect(screen.getByText('$1.00')).toBeInTheDocument()
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

    // Form should show with provider select and cost inputs
    await waitFor(() => {
      expect(screen.getByLabelText('Default Provider')).toBeInTheDocument()
      expect(screen.getByLabelText('Cost Limit Soft ($)')).toBeInTheDocument()
      expect(screen.getByLabelText('Cost Limit Hard ($)')).toBeInTheDocument()
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
})
