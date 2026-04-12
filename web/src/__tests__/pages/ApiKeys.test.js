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

  test('shows scopes as pills', async () => {
    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText('chat')).toBeInTheDocument()
      expect(screen.getByText('agents:read')).toBeInTheDocument()
    })
  })

  test('shows Active status for non-revoked keys', async () => {
    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText('Active')).toBeInTheDocument()
    })
  })

  test('shows Revoked status for revoked keys', async () => {
    server.use(
      http.get('/api/v1/keys', () => HttpResponse.json([
        { id: 'key-r', name: 'old-key', scopes: ['chat'], created_at: '2026-01-01T00:00:00Z', revoked: true },
      ]))
    )

    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText('Revoked')).toBeInTheDocument()
    })
  })

  test('shows Rotate and Revoke buttons for active keys', async () => {
    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText('Rotate')).toBeInTheDocument()
      expect(screen.getByText('Revoke')).toBeInTheDocument()
    })
  })

  test('shows Delete button for revoked keys', async () => {
    server.use(
      http.get('/api/v1/keys', () => HttpResponse.json([
        { id: 'key-r', name: 'old-key', scopes: ['chat'], created_at: '2026-01-01T00:00:00Z', revoked: true },
      ]))
    )

    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText('Delete')).toBeInTheDocument()
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

  test('opens create modal on button click', async () => {
    render(ApiKeys)
    await waitFor(() => {
      expect(screen.getByText('API Keys')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Create Key'))
    await waitFor(() => {
      expect(screen.getByText('Create API Key')).toBeInTheDocument()
      expect(screen.getByText('Permissions')).toBeInTheDocument()
    })
  })

  test('create modal shows scope preset buttons', async () => {
    render(ApiKeys)
    await waitFor(() => screen.getByText('API Keys'))

    await fireEvent.click(screen.getByText('+ Create Key'))
    await waitFor(() => {
      expect(screen.getByText('Read All')).toBeInTheDocument()
      expect(screen.getByText('Full Access')).toBeInTheDocument()
    })
  })

  test('create modal shows resource groups', async () => {
    render(ApiKeys)
    await waitFor(() => screen.getByText('API Keys'))

    await fireEvent.click(screen.getByText('+ Create Key'))
    await waitFor(() => {
      expect(screen.getByText('Chat')).toBeInTheDocument()
      expect(screen.getByText('Admin')).toBeInTheDocument()
      expect(screen.getByText('Sessions')).toBeInTheDocument()
      expect(screen.getByText('Agents')).toBeInTheDocument()
    })
  })

  test('create modal shows no-permissions warning when no scopes selected', async () => {
    render(ApiKeys)
    await waitFor(() => screen.getByText('API Keys'))

    await fireEvent.click(screen.getByText('+ Create Key'))
    await waitFor(() => {
      expect(screen.getByText(/no permissions/i)).toBeInTheDocument()
    })
  })

  test('generate button is disabled when name is empty or no scopes', async () => {
    render(ApiKeys)
    await waitFor(() => screen.getByText('API Keys'))

    await fireEvent.click(screen.getByText('+ Create Key'))
    await waitFor(() => {
      const generateBtn = screen.getByText('Generate Key')
      expect(generateBtn.closest('button')).toBeDisabled()
    })
  })

  test('clicking Revoke opens confirmation dialog', async () => {
    render(ApiKeys)
    await waitFor(() => screen.getByText('test-key'))

    await fireEvent.click(screen.getByText('Revoke'))
    await waitFor(() => {
      expect(screen.getByText('Revoke Key')).toBeInTheDocument()
      expect(screen.getByText(/cannot be undone/)).toBeInTheDocument()
    })
  })

  test('clicking Rotate opens confirmation dialog', async () => {
    render(ApiKeys)
    await waitFor(() => screen.getByText('test-key'))

    await fireEvent.click(screen.getByText('Rotate'))
    await waitFor(() => {
      expect(screen.getByText('Rotate Key')).toBeInTheDocument()
      expect(screen.getByText(/new key will be issued/)).toBeInTheDocument()
    })
  })

  test('confirming revoke calls API and refreshes list', async () => {
    let revokeCalled = false
    server.use(
      http.delete('/api/v1/keys/:id', () => {
        revokeCalled = true
        return new HttpResponse(null, { status: 204 })
      })
    )

    render(ApiKeys)
    await waitFor(() => screen.getByText('test-key'))

    await fireEvent.click(screen.getByText('Revoke'))
    await waitFor(() => screen.getByText('Revoke Key'))

    // Click the Revoke button in the confirm dialog
    const revokeButtons = screen.getAllByText('Revoke')
    await fireEvent.click(revokeButtons[revokeButtons.length - 1])

    await waitFor(() => {
      expect(revokeCalled).toBe(true)
    })
  })

  test('confirming rotate calls API and shows new key', async () => {
    server.use(
      http.post('/api/v1/keys/:id/rotate', () =>
        HttpResponse.json({ key: 'dk_rotated_abc123' })
      )
    )

    render(ApiKeys)
    await waitFor(() => screen.getByText('test-key'))

    await fireEvent.click(screen.getByText('Rotate'))
    await waitFor(() => screen.getByText('Rotate Key'))

    // Click Rotate in confirm dialog
    const rotateButtons = screen.getAllByText('Rotate')
    await fireEvent.click(rotateButtons[rotateButtons.length - 1])

    await waitFor(() => {
      expect(screen.getByText('dk_rotated_abc123')).toBeInTheDocument()
      expect(screen.getByText(/Key rotated/)).toBeInTheDocument()
    })
  })

  test('last used shows dash when null', async () => {
    render(ApiKeys)
    await waitFor(() => {
      // The fixture key has no last_used_at
      expect(screen.getByText('—')).toBeInTheDocument()
    })
  })

  test('create key flow: fill name, set Full Access, submit', async () => {
    render(ApiKeys)
    await waitFor(() => screen.getByText('API Keys'))

    await fireEvent.click(screen.getByText('+ Create Key'))
    await waitFor(() => screen.getByText('Create API Key'))

    // Fill name
    const nameInput = screen.getByPlaceholderText('e.g. my-client')
    await fireEvent.input(nameInput, { target: { value: 'new-api-key' } })

    // Click Full Access preset
    await fireEvent.click(screen.getByText('Full Access'))

    // The warning should disappear since scopes are now set
    await waitFor(() => {
      expect(screen.queryByText(/no permissions/i)).not.toBeInTheDocument()
    })

    // Generate button should now be enabled
    const generateBtn = screen.getByText('Generate Key')
    expect(generateBtn.closest('button')).not.toBeDisabled()

    // Submit
    await fireEvent.click(generateBtn)

    // Should show the new key
    await waitFor(() => {
      expect(screen.getByText('dk_newkey123')).toBeInTheDocument()
      expect(screen.getByText(/copy it now/i)).toBeInTheDocument()
    })
  })
})
