import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Tools from '../../pages/Tools.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')

  // Tools page expects { tools: [...] } wrapper
  server.use(
    http.get('/api/v1/tools', () =>
      HttpResponse.json({
        tools: [
          { name: 'web_search', type: 'stdio', command: 'search', status: 'connected' },
        ],
      })
    )
  )
})

describe('Tools page', () => {
  test('renders page title', async () => {
    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('MCP Tools')).toBeInTheDocument()
    })
  })

  test('renders tool cards with data', async () => {
    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('web_search')).toBeInTheDocument()
    })
  })

  test('shows empty state when no tools', async () => {
    server.use(
      http.get('/api/v1/tools', () => HttpResponse.json({ tools: [] }))
    )

    render(Tools)
    await waitFor(() => {
      expect(screen.getByText(/No MCP tools/)).toBeInTheDocument()
    })
  })

  test('error in tool list falls back gracefully', async () => {
    server.use(
      http.get('/api/v1/tools', () =>
        HttpResponse.json({ error: 'Tool load failed' }, { status: 500 })
      )
    )

    render(Tools)
    // The page catches list errors and shows empty state
    await waitFor(() => {
      expect(screen.getByText(/No MCP tools/)).toBeInTheDocument()
    })
  })
})
