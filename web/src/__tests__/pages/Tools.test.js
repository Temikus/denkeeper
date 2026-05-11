import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Tools from '../../pages/Tools.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')

  server.use(
    http.get('/api/v1/tools', () =>
      HttpResponse.json({
        tools: [
          { name: 'web_search', type: 'stdio', command: 'search-cli', status: 'connected', tool_names: ['search_web', 'search_news', 'search_images'] },
          { name: 'slack_bot', type: 'sse', url: 'http://localhost:9000', status: 'error', last_error: 'connection refused' },
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

  test('renders tool names', async () => {
    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('web_search')).toBeInTheDocument()
      expect(screen.getByText('slack_bot')).toBeInTheDocument()
    })
  })

  test('shows connected tools with green status dot', async () => {
    render(Tools)
    await waitFor(() => {
      const dot = document.querySelector('.status-dot.green')
      expect(dot).toBeInTheDocument()
    })
  })

  test('shows error tools with red status dot', async () => {
    render(Tools)
    await waitFor(() => {
      const dot = document.querySelector('.status-dot.red')
      expect(dot).toBeInTheDocument()
    })
  })

  test('groups tools into Connected and Needs attention sections', async () => {
    render(Tools)
    await waitFor(() => {
      const headings = screen.getAllByRole('heading', { level: 3 })
      const texts = headings.map(h => h.textContent.trim())
      expect(texts.some(t => t.startsWith('Connected'))).toBe(true)
      expect(texts.some(t => t.startsWith('Needs attention'))).toBe(true)
    })
  })

  test('shows tool endpoint info', async () => {
    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('search-cli')).toBeInTheDocument()
      expect(screen.getByText('http://localhost:9000')).toBeInTheDocument()
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
    await waitFor(() => {
      expect(screen.getByText(/No MCP tools/)).toBeInTheDocument()
    })
  })

  test('shows add tool button', async () => {
    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('+ Add Tool')).toBeInTheDocument()
    })
  })

  test('opens add tool form on button click', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))

    await fireEvent.click(screen.getByText('+ Add Tool'))
    await waitFor(() => {
      expect(screen.getByText('Add MCP Tool')).toBeInTheDocument()
      expect(screen.getByText('Transport')).toBeInTheDocument()
    })
  })

  test('shows tool count as clickable link', async () => {
    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('3 tools')).toBeInTheDocument()
    })
  })

  test('shows Retry button for errored tools', async () => {
    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('Retry')).toBeInTheDocument()
    })
  })

  test('kebab menu contains Edit and Remove actions', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('web_search'))

    const kebabBtns = screen.getAllByTitle('More actions')
    await fireEvent.click(kebabBtns[0])

    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
      expect(screen.getByText('Remove')).toBeInTheDocument()
    })
  })

  test('shows Plugins section', async () => {
    server.use(
      http.get('/api/v1/plugins', () =>
        HttpResponse.json({ plugins: [{ name: 'my-plugin', type: 'subprocess', status: 'running' }] })
      )
    )

    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('Plugins')).toBeInTheDocument()
      expect(screen.getByText('my-plugin')).toBeInTheDocument()
    })
  })
})

describe('Tools add form', () => {
  test('stdio form shows Command and Arguments fields', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => {
      expect(screen.getByPlaceholderText('e.g. web-search')).toBeInTheDocument()
      expect(screen.getByPlaceholderText('Path to MCP server binary')).toBeInTheDocument()
      expect(screen.getByPlaceholderText('--provider tavily')).toBeInTheDocument()
    })
  })

  test('switching to SSE transport shows URL field', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => screen.getByText('Transport'))

    const select = screen.getByText('Transport').closest('label').querySelector('select')
    await fireEvent.change(select, { target: { value: 'sse' } })

    await waitFor(() => {
      expect(screen.getByPlaceholderText('https://mcp-server.example.com/sse')).toBeInTheDocument()
    })
  })

  test('Add Tool button is disabled when name is empty', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => {
      // The submit button in the form (not the page header button)
      const addBtns = screen.getAllByText('Add Tool')
      const formBtn = addBtns.find(b => b.closest('.form-actions'))
      expect(formBtn).toBeDisabled()
    })
  })

  test('filling name and command enables Add Tool button', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => screen.getByPlaceholderText('e.g. web-search'))

    await fireEvent.input(screen.getByPlaceholderText('e.g. web-search'), { target: { value: 'my-tool' } })
    await fireEvent.input(screen.getByPlaceholderText('Path to MCP server binary'), { target: { value: '/usr/bin/tool' } })

    await waitFor(() => {
      const addBtns = screen.getAllByText('Add Tool')
      const formBtn = addBtns.find(b => b.closest('.form-actions'))
      expect(formBtn).not.toBeDisabled()
    })
  })

  test('submitting add form calls API and reloads list', async () => {
    let addCalled = false
    let addBody = null
    server.use(
      http.post('/api/v1/tools', async ({ request }) => {
        addCalled = true
        addBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )

    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => screen.getByPlaceholderText('e.g. web-search'))

    await fireEvent.input(screen.getByPlaceholderText('e.g. web-search'), { target: { value: 'new-tool' } })
    await fireEvent.input(screen.getByPlaceholderText('Path to MCP server binary'), { target: { value: '/bin/tool' } })

    const addBtns = screen.getAllByText('Add Tool')
    const formBtn = addBtns.find(b => b.closest('.form-actions'))
    await fireEvent.click(formBtn)

    await waitFor(() => {
      expect(addCalled).toBe(true)
      expect(addBody.name).toBe('new-tool')
      expect(addBody.command).toBe('/bin/tool')
    })
  })

  test('env var + Add button adds a key/value row', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => {
      const labels = screen.getAllByText('Environment Variables')
      expect(labels.length).toBeGreaterThan(0)
    })

    // Click the first "+ Add" button inside an env-section (the tool form one)
    const envSections = document.querySelectorAll('.env-section')
    const addBtn = envSections[0].querySelector('.btn-sm')
    await fireEvent.click(addBtn)

    await waitFor(() => {
      expect(screen.getByPlaceholderText('Key')).toBeInTheDocument()
      expect(screen.getByPlaceholderText('Value')).toBeInTheDocument()
    })
  })

  test('env var x button removes a row', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => {
      const labels = screen.getAllByText('Environment Variables')
      expect(labels.length).toBeGreaterThan(0)
    })

    // Add a row first
    const envSections = document.querySelectorAll('.env-section')
    const addBtn = envSections[0].querySelector('.btn-sm')
    await fireEvent.click(addBtn)

    await waitFor(() => screen.getByPlaceholderText('Key'))

    // Click the x button (danger btn inside env-row) to remove
    const removeBtn = document.querySelector('.env-row .btn-sm.danger')
    await fireEvent.click(removeBtn)

    await waitFor(() => {
      expect(screen.queryByPlaceholderText('Key')).not.toBeInTheDocument()
    })
  })

  test('cancel button closes the form', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => screen.getByText('Add MCP Tool'))

    // Find the Cancel button inside the form-actions
    const cancelBtn = document.querySelector('.form-actions .btn-ghost')
    await fireEvent.click(cancelBtn)

    // The inline panel should close (CSS hides it via class:open)
    await waitFor(() => {
      const panel = document.querySelector('.inline-panel')
      expect(panel.classList.contains('open')).toBe(false)
    })
  })
})

describe('Tools edit form', () => {
  test('clicking Edit in kebab menu loads tool config into inline form', async () => {
    server.use(
      http.get('/api/v1/tools/:name', () =>
        HttpResponse.json({
          name: 'web_search',
          transport: 'stdio',
          command: 'search-cli',
          args: ['--provider', 'tavily'],
          env: { API_KEY: 'secret' },
        })
      )
    )

    render(Tools)
    await waitFor(() => screen.getByText('web_search'))

    const kebabBtns = screen.getAllByTitle('More actions')
    await fireEvent.click(kebabBtns[0])

    await waitFor(() => screen.getByText('Edit'))
    await fireEvent.click(screen.getByText('Edit'))

    await waitFor(() => {
      expect(screen.getByText('Save Changes')).toBeInTheDocument()
    })
  })
})

describe('Tools retry and remove', () => {
  test('Retry button calls restart API', async () => {
    let restartCalled = false
    server.use(
      http.post('/api/v1/tools/:name/restart', () => {
        restartCalled = true
        return HttpResponse.json({ ok: true })
      })
    )

    render(Tools)
    await waitFor(() => screen.getByText('Retry'))

    await fireEvent.click(screen.getByText('Retry'))

    await waitFor(() => {
      expect(restartCalled).toBe(true)
    })
  })

  test('Remove in kebab menu opens confirm dialog', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('web_search'))

    const kebabBtns = screen.getAllByTitle('More actions')
    await fireEvent.click(kebabBtns[0])

    await waitFor(() => screen.getByText('Remove'))
    await fireEvent.click(screen.getByText('Remove'))

    await waitFor(() => {
      const overlay = document.querySelector('.overlay')
      expect(overlay).toBeInTheDocument()
      const strong = overlay.querySelector('strong')
      expect(strong.textContent).toBe('web_search')
    })
  })

  test('shows Connection failed detail for errored tools', async () => {
    render(Tools)
    await waitFor(() => {
      expect(screen.getByText('Connection failed')).toBeInTheDocument()
      expect(screen.getByText('connection refused')).toBeInTheDocument()
    })
  })
})

describe('Tools SSE transport fields', () => {
  test('SSE form shows OAuth toggle', async () => {
    render(Tools)
    await waitFor(() => screen.getByText('+ Add Tool'))
    await fireEvent.click(screen.getByText('+ Add Tool'))

    await waitFor(() => screen.getByText('Transport'))

    const select = screen.getByText('Transport').closest('label').querySelector('select')
    await fireEvent.change(select, { target: { value: 'sse' } })

    await waitFor(() => {
      expect(screen.getByText('OAuth 2.1')).toBeInTheDocument()
      expect(screen.getByText('Connection settings')).toBeInTheDocument()
    })
  })
})
