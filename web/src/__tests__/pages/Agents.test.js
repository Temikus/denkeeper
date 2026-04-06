import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/svelte'
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
      expect(screen.getByText('default')).toBeInTheDocument()
    })

    await waitFor(() => {
      expect(screen.getByText('Persona')).toBeInTheDocument()
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
})
