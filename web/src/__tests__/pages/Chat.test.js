import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'

// Mock wsStore before importing Chat (it imports wsStore via chatStore).
const { writable } = await import('svelte/store')
const mockWsStatus = writable('disconnected')
const mockPanicStatus = writable({ active: false, message: '' })

vi.mock('../../wsStore.js', () => ({
  wsStatus: mockWsStatus,
  panicStatus: mockPanicStatus,
  initWS: vi.fn(),
  destroyWS: vi.fn(),
  getWSClient: vi.fn(() => ({ send: vi.fn(() => true) })),
  onSessionEvent: vi.fn(),
  offSessionEvent: vi.fn(),
  onActivity: vi.fn(() => vi.fn()),
}))

const { chatState, newSession, setChannel } = await import('../../chatStore.js')
const Chat = (await import('../../pages/Chat.svelte')).default

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  mockWsStatus.set('disconnected')
  // Reset chatStore state between tests
  newSession()
  chatState.update(s => ({ ...s, initialized: false, sending: false, error: '' }))
  vi.useFakeTimers({ shouldAdvanceTime: true })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('Chat page', () => {
  test('renders empty state', async () => {
    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Send a message to start a conversation.')).toBeInTheDocument()
    })
  })

  test('agent selector is populated from API', async () => {
    render(Chat)
    await waitFor(() => {
      // Wait for agents to load — "helper" is unique to the agent selector
      expect(screen.getByText('helper')).toBeInTheDocument()
    })
    // Both agent options should be present
    const agentLabel = screen.getByText('Agent')
    const agentSelect = agentLabel.closest('label').querySelector('select')
    const options = agentSelect.querySelectorAll('option')
    const optionTexts = Array.from(options).map(o => o.textContent)
    expect(optionTexts).toContain('default')
    expect(optionTexts).toContain('helper')
  })

  test('session dropdown shows sessions', async () => {
    render(Chat)
    await waitFor(() => {
      const options = screen.getAllByRole('option')
      const sessOption = options.find(o => o.textContent.includes('sess-1'))
      expect(sessOption).toBeTruthy()
    })
  })

  test('send button is disabled when input is empty', async () => {
    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Send')).toBeInTheDocument()
    })
    expect(document.querySelector('.btn-send')).toBeDisabled()
  })

  test('sends message and shows streaming response', async () => {
    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Send a message to start a conversation.')).toBeInTheDocument()
    })

    const textarea = screen.getByPlaceholderText(/Type a message/)
    await fireEvent.input(textarea, { target: { value: 'Hello' } })
    await fireEvent.click(document.querySelector('.btn-send'))

    await waitFor(() => {
      expect(screen.getByText('Hello')).toBeInTheDocument()
    })

    // The SSE handler returns "Hello" content then done
    await waitFor(() => {
      const bubbles = document.querySelectorAll('.bubble.agent')
      expect(bubbles.length).toBeGreaterThanOrEqual(1)
    })
  })

  test('shows tool call cards during streaming', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"tool_start","tool":"web_search","round":1}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"tool_end","tool":"web_search","round":1,"duration_ms":200}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"content","text":"Search result"}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-new"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    render(Chat)
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/Type a message/)).toBeInTheDocument()
    })

    const textarea = screen.getByPlaceholderText(/Type a message/)
    await fireEvent.input(textarea, { target: { value: 'search something' } })
    await fireEvent.click(document.querySelector('.btn-send'))

    await waitFor(() => {
      // Tool call card should show the tool name
      const toolCalls = document.querySelectorAll('.tool-call')
      expect(toolCalls.length).toBeGreaterThanOrEqual(1)
      expect(toolCalls[0].textContent).toContain('web_search')
    })
  })

  test('shows pending approval with action buttons', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"tool_approval","approval_id":"a1","tool":"web_search","text":"search","approval_status":"pending"}\n\n'))
            // Don't send done yet — approval is pending
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    render(Chat)
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/Type a message/)).toBeInTheDocument()
    })

    const textarea = screen.getByPlaceholderText(/Type a message/)
    await fireEvent.input(textarea, { target: { value: 'search' } })
    await fireEvent.click(document.querySelector('.btn-send'))

    await waitFor(() => {
      // Inline approval buttons should appear
      const approveButtons = screen.getAllByText('Approve')
      expect(approveButtons.length).toBeGreaterThanOrEqual(1)
      expect(screen.getAllByText('Deny').length).toBeGreaterThanOrEqual(1)
      expect(screen.getAllByText('15 min').length).toBeGreaterThanOrEqual(1)
      expect(screen.getAllByText('Always').length).toBeGreaterThanOrEqual(1)
    })
  })

  test('Enter sends message, Shift+Enter does not', async () => {
    render(Chat)
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/Type a message/)).toBeInTheDocument()
    })

    const textarea = screen.getByPlaceholderText(/Type a message/)
    await fireEvent.input(textarea, { target: { value: 'test' } })

    // Shift+Enter should NOT send — no user bubble should appear
    await fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: true })
    expect(document.querySelectorAll('.bubble.user').length).toBe(0)

    // Enter should send — user bubble appears
    await fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: false })
    await waitFor(() => {
      expect(document.querySelectorAll('.bubble.user').length).toBeGreaterThanOrEqual(1)
    })
  })

  test('textarea stays enabled while sending and stop button appears', async () => {
    let resolveStream
    server.use(
      http.post('/api/v1/chat', () => {
        const stream = new ReadableStream({
          start(controller) {
            resolveStream = () => {
              const encoder = new TextEncoder()
              controller.enqueue(encoder.encode('data: {"type":"content","text":"done"}\n\n'))
              controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-1"}\n\n'))
              controller.close()
            }
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    render(Chat)
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/Type a message/)).toBeInTheDocument()
    })

    const textarea = screen.getByPlaceholderText(/Type a message/)
    await fireEvent.input(textarea, { target: { value: 'hello' } })
    await fireEvent.click(document.querySelector('.btn-send'))

    // Textarea stays enabled so user can compose next message.
    await waitFor(() => {
      expect(textarea).not.toBeDisabled()
    })
    // Stop button replaces send button while sending.
    expect(document.querySelector('.btn-stop')).toBeInTheDocument()
    expect(document.querySelector('.btn-send')).not.toBeInTheDocument()

    resolveStream()

    // After stream completes, sending state clears — send button returns.
    await waitFor(() => {
      expect(textarea).not.toBeDisabled()
    })
    // Typing new text shows the send button again.
    await fireEvent.input(textarea, { target: { value: 'next' } })
    await waitFor(() => {
      expect(document.querySelector('.btn-send')).not.toBeDisabled()
    })
  })

  test('error state shows error bar', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"error","message":"LLM unavailable"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    render(Chat)
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/Type a message/)).toBeInTheDocument()
    })

    const textarea = screen.getByPlaceholderText(/Type a message/)
    await fireEvent.input(textarea, { target: { value: 'hello' } })
    await fireEvent.click(document.querySelector('.btn-send'))

    await waitFor(() => {
      expect(document.querySelector('.error-bar')).toBeInTheDocument()
    })
  })

  test('New Session button clears messages', async () => {
    render(Chat)
    await waitFor(() => {
      expect(screen.getByPlaceholderText(/Type a message/)).toBeInTheDocument()
    })

    // Send a message first
    const textarea = screen.getByPlaceholderText(/Type a message/)
    await fireEvent.input(textarea, { target: { value: 'test message for clear' } })
    await fireEvent.click(document.querySelector('.btn-send'))

    await waitFor(() => {
      expect(document.querySelectorAll('.bubble.user').length).toBeGreaterThanOrEqual(1)
    })

    // Click New Session
    const newSessionBtn = screen.getByText('New Session')
    await fireEvent.click(newSessionBtn)

    await waitFor(() => {
      expect(document.querySelectorAll('.bubble').length).toBe(0)
    })
  })

  test('WS status indicator shows connection state', async () => {
    render(Chat)
    await waitFor(() => {
      expect(document.querySelector('.ws-status')).toBeInTheDocument()
    })

    // Default disconnected — no visible text besides the dot
    mockWsStatus.set('connected')
    await waitFor(() => {
      expect(screen.getByText('WS')).toBeInTheDocument()
    })
  })

  test('channel selector appears when channels are available', async () => {
    render(Chat)
    await waitFor(() => {
      // Channels endpoint returns 4 channels, but only 3 are non-implicit
      const channelLabel = screen.getByText('Channel')
      const channelSelect = channelLabel.closest('label').querySelector('select')
      const options = channelSelect.querySelectorAll('option')
      // "None" + 3 non-implicit channels (work, personal, scratch)
      expect(options.length).toBe(4)
      expect(Array.from(options).map(o => o.textContent)).toContain('work')
      expect(Array.from(options).map(o => o.textContent)).toContain('personal')
    })
  })

  test('ephemeral channel shows indicator in selector', async () => {
    render(Chat)
    await waitFor(() => {
      const channelLabel = screen.getByText('Channel')
      const channelSelect = channelLabel.closest('label').querySelector('select')
      const options = Array.from(channelSelect.querySelectorAll('option'))
      const scratchOption = options.find(o => o.value === 'scratch')
      expect(scratchOption).toBeTruthy()
      expect(scratchOption.textContent).toContain('(ephemeral)')
    })
  })

  test('channel selector hidden when no channels', async () => {
    server.use(
      http.get('/api/v1/channels', () => HttpResponse.json([]))
    )

    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Agent')).toBeInTheDocument()
    })
    // Wait a tick for channels to load (empty)
    await waitFor(() => {
      expect(screen.queryByText('Channel')).not.toBeInTheDocument()
    })
  })

  test('pending approvals banner appears from polled data', async () => {
    server.use(
      http.get('/api/v1/approvals', ({ request }) => {
        const url = new URL(request.url)
        if (url.searchParams.get('status') === 'pending') {
          return HttpResponse.json([
            { id: 'appr-poll-1', agent_name: 'default', adapter_name: 'telegram', external_id: '12345678', kind: 'tool_call', status: 'pending', summary: 'Run tool: fetch_data' },
          ])
        }
        return HttpResponse.json([])
      })
    )

    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Pending approvals (1)')).toBeInTheDocument()
      expect(screen.getByText('Run tool: fetch_data')).toBeInTheDocument()
    })
  })

  // --- Channel selector interaction tests ---

  test('switching channel calls activate API', async () => {
    let activateCalled = { name: null, key: null }
    server.use(
      http.post('/api/v1/channels/:name/activate', async ({ params, request }) => {
        const body = await request.json()
        activateCalled = { name: params.name, key: body.adapter_key }
        return HttpResponse.json({ status: 'activated', channel: params.name, adapter_key: body.adapter_key })
      })
    )

    render(Chat)
    await waitFor(() => {
      const channelLabel = screen.getByText('Channel')
      expect(channelLabel).toBeInTheDocument()
    })

    const channelSelect = screen.getByText('Channel').closest('label').querySelector('select')
    await fireEvent.change(channelSelect, { target: { value: 'work' } })

    await waitFor(() => {
      expect(activateCalled.name).toBe('work')
      expect(activateCalled.key).toBe('api:web-dashboard')
    })
  })

  test('switching away from channel calls deactivate then activate', async () => {
    let deactivateCalled = null
    let activateName = null
    server.use(
      http.post('/api/v1/channels/:name/activate', async ({ params, request }) => {
        const body = await request.json()
        activateName = params.name
        return HttpResponse.json({ status: 'activated', channel: params.name, adapter_key: body.adapter_key })
      }),
      http.delete('/api/v1/channels/:name/activate', async ({ params }) => {
        deactivateCalled = params.name
        return HttpResponse.json({ status: 'deactivated' })
      }),
    )

    // Pre-set channel state to "work" so the switch triggers deactivation
    setChannel('work')

    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Channel')).toBeInTheDocument()
    })

    const channelSelect = screen.getByText('Channel').closest('label').querySelector('select')
    await fireEvent.change(channelSelect, { target: { value: 'personal' } })

    await waitFor(() => {
      expect(deactivateCalled).toBe('work')
      expect(activateName).toBe('personal')
    })
  })

  test('switching to channel with different agent updates agent', async () => {
    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Channel')).toBeInTheDocument()
    })

    const channelSelect = screen.getByText('Channel').closest('label').querySelector('select')
    // "personal" channel has agent "helper", different from default
    await fireEvent.change(channelSelect, { target: { value: 'personal' } })

    await waitFor(() => {
      const agentSelect = screen.getByText('Agent').closest('label').querySelector('select')
      expect(agentSelect.value).toBe('helper')
    })
  })

  test('selecting None deactivates current channel', async () => {
    let deactivateCalled = null
    server.use(
      http.delete('/api/v1/channels/:name/activate', async ({ params }) => {
        deactivateCalled = params.name
        return HttpResponse.json({ status: 'deactivated' })
      }),
    )

    // Pre-set channel state to "work"
    setChannel('work')

    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Channel')).toBeInTheDocument()
    })

    const channelSelect = screen.getByText('Channel').closest('label').querySelector('select')
    await fireEvent.change(channelSelect, { target: { value: '' } })

    await waitFor(() => {
      expect(deactivateCalled).toBe('work')
    })
  })

  test('channel switch error shows error', async () => {
    server.use(
      http.post('/api/v1/channels/:name/activate', () =>
        HttpResponse.json({ error: 'activation failed' }, { status: 500 })
      ),
    )

    render(Chat)
    await waitFor(() => {
      expect(screen.getByText('Channel')).toBeInTheDocument()
    })

    const channelSelect = screen.getByText('Channel').closest('label').querySelector('select')
    await fireEvent.change(channelSelect, { target: { value: 'work' } })

    await waitFor(() => {
      expect(document.querySelector('.error-bar')).toBeInTheDocument()
    })
  })
})
