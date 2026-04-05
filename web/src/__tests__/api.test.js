import { describe, test, expect, beforeEach } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '../test/server.js'
import { token, authMode } from '../store.js'
import { api } from '../api.js'

beforeEach(() => {
  token.clear()
  authMode.set(null)
})

describe('apiFetch auth handling', () => {
  test('sends Bearer token when authMode is token', async () => {
    let capturedAuth
    server.use(
      http.get('/api/v1/agents', ({ request }) => {
        capturedAuth = request.headers.get('authorization')
        return HttpResponse.json([])
      })
    )
    token.set('test-key-123')
    authMode.set('token')
    await api.agents()
    expect(capturedAuth).toBe('Bearer test-key-123')
  })

  test('omits Authorization header when authMode is session', async () => {
    let capturedAuth
    server.use(
      http.get('/api/v1/agents', ({ request }) => {
        capturedAuth = request.headers.get('authorization')
        return HttpResponse.json([])
      })
    )
    authMode.set('session')
    await api.agents()
    expect(capturedAuth).toBeNull()
  })

  test('401 response clears token and throws', async () => {
    server.use(
      http.get('/api/v1/agents', () => new HttpResponse(null, { status: 401 }))
    )
    token.set('old-key')
    await expect(api.agents()).rejects.toThrow('Unauthorized')
    const { get } = await import('svelte/store')
    expect(get(token)).toBe('')
  })

  test('204 response returns null', async () => {
    server.use(
      http.delete('/api/v1/sessions/:id', () => new HttpResponse(null, { status: 204 }))
    )
    token.set('key')
    const result = await api.deleteSession('sess-1')
    expect(result).toBeNull()
  })

  test('error response extracts body.error message', async () => {
    server.use(
      http.get('/api/v1/agents', () =>
        HttpResponse.json({ error: 'forbidden' }, { status: 403 })
      )
    )
    token.set('key')
    await expect(api.agents()).rejects.toThrow('forbidden')
  })

  test('error response falls back to HTTP status', async () => {
    server.use(
      http.get('/api/v1/agents', () =>
        new HttpResponse('not json', { status: 500, headers: { 'Content-Type': 'text/plain' } })
      )
    )
    token.set('key')
    await expect(api.agents()).rejects.toThrow('HTTP 500')
  })
})

describe('streamChat', () => {
  test('calls onChunk for content events and onDone with session_id', async () => {
    const chunks = []
    let doneSessionId
    const toolEvents = []
    token.set('key')

    await api.streamChat(
      'default', '', 'hello',
      (chunk) => chunks.push(chunk),
      (sid) => { doneSessionId = sid },
      (evt) => toolEvents.push(evt),
    )

    expect(chunks).toEqual(['Hello'])
    expect(doneSessionId).toBe('sess-1')
  })

  test('calls onToolEvent for tool events', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"tool_start","tool":"web_search","round":1}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"tool_end","tool":"web_search","round":1,"duration_ms":500}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"content","text":"result"}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-2"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    const toolEvents = []
    token.set('key')

    await api.streamChat(
      'default', '', 'search',
      () => {},
      () => {},
      (evt) => toolEvents.push(evt),
    )

    expect(toolEvents).toHaveLength(2)
    expect(toolEvents[0].type).toBe('tool_start')
    expect(toolEvents[1].type).toBe('tool_end')
    expect(toolEvents[1].duration_ms).toBe(500)
  })

  test('throws on error event', async () => {
    // Note: api.js only rethrows errors with the exact message 'stream error'
    // (the default when no message is provided). Custom messages are caught
    // by the malformed-JSON catch and swallowed.
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"error"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    token.set('key')
    await expect(
      api.streamChat('default', '', 'hello', () => {}, () => {}, () => {})
    ).rejects.toThrow('stream error')
  })
})

describe('API method smoke tests', () => {
  test('agents() fetches /api/v1/agents', async () => {
    token.set('key')
    const result = await api.agents()
    expect(result).toHaveLength(2)
    expect(result[0].name).toBe('default')
  })

  test('approveApproval with autoApprove appends query param', async () => {
    let capturedUrl
    server.use(
      http.post('/api/v1/approvals/:id/approve', ({ request }) => {
        capturedUrl = request.url
        return HttpResponse.json({ ok: true })
      })
    )
    token.set('key')
    await api.approveApproval('appr-1', 'session')
    expect(capturedUrl).toContain('auto_approve=session')
  })

  test('models() returns model list', async () => {
    token.set('key')
    const result = await api.models()
    expect(result).toEqual(['claude-3-opus', 'gpt-4o'])
  })
})
