import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Channels from '../../pages/Channels.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Channels page', () => {
  test('renders page title', () => {
    render(Channels)
    expect(screen.getByText('Channels')).toBeInTheDocument()
  })

  test('renders channel list', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
      expect(screen.getByText('personal')).toBeInTheDocument()
    })
  })

  test('shows empty state when no channels', async () => {
    server.use(
      http.get('/api/v1/channels', () => HttpResponse.json([]))
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText(/No channels configured/)).toBeInTheDocument()
    })
  })

  test('shows select prompt before clicking a channel', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('Select a channel to view details.')).toBeInTheDocument()
    })
  })

  test('clicking channel shows detail', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('chan:work')).toBeInTheDocument()
      expect(screen.getByText('Explicit')).toBeInTheDocument()
    })
  })

  test('implicit channel shows Implicit badge', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('default-telegram')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('default-telegram').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Implicit')).toBeInTheDocument()
    })
  })

  test('shows adapter pills in detail', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('telegram')).toBeInTheDocument()
      expect(screen.getByText('telegram:387956986')).toBeInTheDocument()
    })
  })

  test('shows session mode in detail', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      // "Session Mode" appears in both the form (collapsed) and detail panel;
      // verify the detail panel's field label is present.
      const labels = screen.getAllByText('Session Mode')
      expect(labels.length).toBeGreaterThanOrEqual(1)
      expect(screen.getByText('persistent')).toBeInTheDocument()
    })
  })

  test('ephemeral channel shows ephemeral session mode', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('scratch')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('scratch').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('ephemeral')).toBeInTheDocument()
      expect(screen.getByText('(generated per interaction)')).toBeInTheDocument()
    })
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/channels', () =>
        HttpResponse.json({ error: 'Database error' }, { status: 500 })
      )
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('Database error')).toBeInTheDocument()
    })
  })

  // --- Activate/Deactivate tests ---

  test('activate form renders for explicit channel', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Activate Adapter')).toBeInTheDocument()
      expect(screen.getByPlaceholderText('adapter:externalID')).toBeInTheDocument()
      expect(screen.getByText('Activate')).toBeInTheDocument()
    })
  })

  test('activate form hidden for implicit channel', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('default-telegram')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('default-telegram').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Implicit')).toBeInTheDocument()
    })
    expect(screen.queryByText('Activate Adapter')).not.toBeInTheDocument()
  })

  test('activate button disabled when input empty', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Activate')).toBeInTheDocument()
    })
    expect(screen.getByText('Activate')).toBeDisabled()
  })

  test('successful activation refreshes channel detail', async () => {
    let activateCalled = false
    server.use(
      http.post('/api/v1/channels/:name/activate', async () => {
        activateCalled = true
        return HttpResponse.json({ status: 'activated', channel: 'personal', adapter_key: 'api:test' })
      }),
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('personal')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('personal').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Activate Adapter')).toBeInTheDocument()
    })

    const input = screen.getByPlaceholderText('adapter:externalID')
    await fireEvent.input(input, { target: { value: 'api:test' } })
    await fireEvent.click(screen.getByText('Activate'))

    await waitFor(() => {
      expect(activateCalled).toBe(true)
    })
  })

  test('input clears after successful activation', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('personal')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('personal').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Activate Adapter')).toBeInTheDocument()
    })

    const input = screen.getByPlaceholderText('adapter:externalID')
    await fireEvent.input(input, { target: { value: 'api:test' } })
    await fireEvent.click(screen.getByText('Activate'))

    await waitFor(() => {
      expect(input.value).toBe('')
    })
  })

  test('activate shows format error for invalid key', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Activate Adapter')).toBeInTheDocument()
    })

    const input = screen.getByPlaceholderText('adapter:externalID')
    await fireEvent.input(input, { target: { value: 'badformat' } })
    await fireEvent.click(screen.getByText('Activate'))

    await waitFor(() => {
      const alert = screen.getByRole('alert')
      expect(alert).toBeInTheDocument()
      expect(alert.textContent).toContain('Format: adapter:externalID')
    })
  })

  test('deactivate button on active key pills for explicit channel', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('telegram:387956986')).toBeInTheDocument()
    })
    expect(screen.getByLabelText('Deactivate telegram:387956986')).toBeInTheDocument()
  })

  test('clicking deactivate pill calls API directly', async () => {
    let deactivateCalled = false
    server.use(
      http.delete('/api/v1/channels/:name/activate', () => {
        deactivateCalled = true
        return HttpResponse.json({ status: 'deactivated' })
      }),
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByLabelText('Deactivate telegram:387956986')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByLabelText('Deactivate telegram:387956986'))
    await waitFor(() => {
      expect(deactivateCalled).toBe(true)
    })
  })

  test('activate shows error on API failure', async () => {
    server.use(
      http.post('/api/v1/channels/:name/activate', () =>
        HttpResponse.json({ error: 'channel not found' }, { status: 404 })
      ),
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Activate Adapter')).toBeInTheDocument()
    })

    const input = screen.getByPlaceholderText('adapter:externalID')
    await fireEvent.input(input, { target: { value: 'api:test' } })
    await fireEvent.click(screen.getByText('Activate'))

    await waitFor(() => {
      expect(screen.getByText('channel not found')).toBeInTheDocument()
    })
  })

  test('implicit channel has no deactivate buttons on pills', async () => {
    // Override to give the implicit channel an active key for this test
    server.use(
      http.get('/api/v1/channels', () => HttpResponse.json([
        {
          name: 'default-telegram',
          agent: 'default',
          adapters: ['telegram'],
          implicit: true,
          session_mode: 'persistent',
          conversation_id: 'chan:default-telegram',
          active_adapter_keys: ['telegram:12345'],
        },
      ]))
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('default-telegram')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('default-telegram').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('telegram:12345')).toBeInTheDocument()
    })
    expect(screen.queryByLabelText(/Deactivate/)).not.toBeInTheDocument()
  })

  // --- CRUD tests ---

  test('add button renders in header', () => {
    render(Channels)
    expect(screen.getByText('+ Add Channel')).toBeInTheDocument()
  })

  test('add button opens inline form', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByRole('button', { name: '+ Add Channel' })).toBeInTheDocument()
    })
    const addBtn = screen.getByRole('button', { name: '+ Add Channel' })
    await fireEvent.click(addBtn)
    await waitFor(() => {
      expect(screen.getByText('Add Channel', { selector: '.form-title' })).toBeInTheDocument()
    })
  })

  test('create form submits to API', async () => {
    let createCalled = false
    server.use(
      http.post('/api/v1/channels', async ({ request }) => {
        createCalled = true
        const body = await request.json()
        return HttpResponse.json({ name: body.name, agent: body.agent, adapters: [], implicit: false, conversation_id: `chan:${body.name}`, active_adapter_keys: [] }, { status: 201 })
      }),
    )

    render(Channels)
    // Wait for agents to load
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Channel'))
    await waitFor(() => {
      expect(screen.getByLabelText('Name')).toBeInTheDocument()
    })

    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'test-ch' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(createCalled).toBe(true)
    })
  })

  test('create form closes after successful save', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Channel'))
    await waitFor(() => {
      expect(screen.getByLabelText('Name')).toBeInTheDocument()
    })

    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'new-ch' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      // The inline-panel collapses (loses .open class) rather than unmounting.
      const panel = document.querySelector('.inline-panel')
      expect(panel).not.toHaveClass('open')
    })
  })

  test('create shows error on API failure', async () => {
    server.use(
      http.post('/api/v1/channels', () =>
        HttpResponse.json({ error: 'channel already exists' }, { status: 409 })
      ),
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Channel'))
    await waitFor(() => {
      expect(screen.getByLabelText('Name')).toBeInTheDocument()
    })

    await fireEvent.input(screen.getByLabelText('Name'), { target: { value: 'dup' } })
    await fireEvent.click(screen.getByText('Save'))

    await waitFor(() => {
      expect(screen.getByText('channel already exists')).toBeInTheDocument()
    })
  })

  test('edit button opens pre-filled form for explicit channel', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText('Edit Channel')).toBeInTheDocument()
      // Name field should be disabled in edit mode
      expect(screen.getByLabelText('Name')).toBeDisabled()
    })
  })

  test('edit/delete buttons hidden for implicit channels', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('default-telegram')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('default-telegram').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Implicit')).toBeInTheDocument()
    })
    expect(screen.queryByText('Edit')).not.toBeInTheDocument()
    expect(screen.queryByText('Delete')).not.toBeInTheDocument()
  })

  test('edit submits PATCH', async () => {
    let patchCalled = false
    server.use(
      http.patch('/api/v1/channels/:name', () => {
        patchCalled = true
        return HttpResponse.json({ name: 'work', agent: 'helper', adapters: ['telegram'], implicit: false, conversation_id: 'chan:work', active_adapter_keys: [] })
      }),
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText('Edit Channel')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Save'))
    await waitFor(() => {
      expect(patchCalled).toBe(true)
    })
  })

  test('delete button shows confirmation modal', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Delete')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Delete'))
    await waitFor(() => {
      expect(screen.getByText('Delete Channel')).toBeInTheDocument()
      expect(screen.getByText(/Active adapter keys will be cleared/)).toBeInTheDocument()
    })
  })

  test('confirming delete calls API', async () => {
    let deleteCalled = false
    server.use(
      http.delete('/api/v1/channels/:name', () => {
        deleteCalled = true
        return new HttpResponse(null, { status: 204 })
      }),
    )

    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Delete')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Delete'))
    await waitFor(() => {
      expect(screen.getByText('Delete Channel')).toBeInTheDocument()
    })

    // Click the Delete button in the modal
    const modal = screen.getByRole('dialog')
    await fireEvent.click(within(modal).getByRole('button', { name: 'Delete' }))

    await waitFor(() => {
      expect(deleteCalled).toBe(true)
    })
  })

  test('cancel in delete modal closes it', async () => {
    render(Channels)
    await waitFor(() => {
      expect(screen.getByText('work')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('work').closest('[role="button"]'))
    await waitFor(() => {
      expect(screen.getByText('Delete')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Delete'))
    await waitFor(() => {
      expect(screen.getByText('Delete Channel')).toBeInTheDocument()
    })

    // Click Cancel within the modal (not the form's Cancel)
    const modal = screen.getByRole('dialog')
    await fireEvent.click(within(modal).getByText('Cancel'))
    await waitFor(() => {
      expect(screen.queryByText('Delete Channel')).not.toBeInTheDocument()
    })
  })
})
