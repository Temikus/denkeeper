import { http, HttpResponse } from 'msw'
import { agents, sessions, messages, approvals } from './fixtures/index.js'

export const handlers = [
  // Health
  http.get('/api/v1/health', () => HttpResponse.json({ status: 'ok' })),

  // Agents
  http.get('/api/v1/agents', () => HttpResponse.json(agents)),

  // Models
  http.get('/api/v1/models', () => HttpResponse.json({ models: ['claude-3-opus', 'gpt-4o'] })),

  // Sessions
  http.get('/api/v1/sessions', () => HttpResponse.json(sessions)),
  http.get('/api/v1/sessions/:id/messages', () => HttpResponse.json(messages)),
  http.delete('/api/v1/sessions/:id', () => new HttpResponse(null, { status: 204 })),

  // Approvals
  http.get('/api/v1/approvals', () => HttpResponse.json(approvals)),
  http.post('/api/v1/approvals/:id/approve', () => HttpResponse.json({ ok: true })),
  http.post('/api/v1/approvals/:id/deny', () => HttpResponse.json({ ok: true })),

  // Chat SSE
  http.post('/api/v1/chat', () => {
    const encoder = new TextEncoder()
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(encoder.encode('data: {"type":"content","text":"Hello"}\n\n'))
        controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-1"}\n\n'))
        controller.close()
      },
    })
    return new HttpResponse(stream, {
      headers: { 'Content-Type': 'text/event-stream' },
    })
  }),

  // Auth
  http.get('/auth/config', () => HttpResponse.json({ mode: 'token' })),
  http.get('/auth/session', () => HttpResponse.json({ authenticated: false })),
  http.post('/auth/logout', () => HttpResponse.json({ ok: true })),
]
