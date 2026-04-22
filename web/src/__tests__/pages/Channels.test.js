import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
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
})
