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

describe('streamChat edge cases', () => {
  test('throws on error event with custom message (bug fix verification)', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"error","message":"rate limit exceeded"}\n\n'))
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
    ).rejects.toThrow('rate limit exceeded')
  })

  test('handles partial frame delivery split across reads', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            // Split a frame across two chunks
            controller.enqueue(encoder.encode('data: {"type":"con'))
            controller.enqueue(encoder.encode('tent","text":"split"}\n\ndata: {"type":"done","session_id":"sess-3"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    const chunks = []
    let doneId
    token.set('key')

    await api.streamChat(
      'default', '', 'hello',
      (chunk) => chunks.push(chunk),
      (sid) => { doneId = sid },
      () => {},
    )

    expect(chunks).toEqual(['split'])
    expect(doneId).toBe('sess-3')
  })

  test('empty text in content event delivers empty string', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"content"}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-4"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    const chunks = []
    token.set('key')

    await api.streamChat(
      'default', '', 'hello',
      (chunk) => chunks.push(chunk),
      () => {},
      () => {},
    )

    expect(chunks).toEqual([''])
  })

  test('stream interrupted without done event calls onDone fallback', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"content","text":"partial"}\n\n'))
            // Close without sending done frame
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    const chunks = []
    let doneSessionId = null
    token.set('key')

    await api.streamChat(
      'default', '', 'hello',
      (chunk) => chunks.push(chunk),
      (sid) => { doneSessionId = sid },
      () => {},
    )

    expect(chunks).toEqual(['partial'])
    // Fallback onDone is called with empty string when stream closes without done event
    expect(doneSessionId).toBe('')
  })

  test('stream with done event does not call fallback onDone', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"content","text":"ok"}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-normal"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    const doneCalls = []
    token.set('key')

    await api.streamChat(
      'default', '', 'hello',
      () => {},
      (sid) => doneCalls.push(sid),
      () => {},
    )

    // onDone should be called exactly once with the real session_id, not twice
    expect(doneCalls).toEqual(['sess-normal'])
  })

  test('skips malformed JSON frames without throwing', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: not-json\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"content","text":"ok"}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-5"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    const chunks = []
    token.set('key')

    await api.streamChat(
      'default', '', 'hello',
      (chunk) => chunks.push(chunk),
      () => {},
      () => {},
    )

    expect(chunks).toEqual(['ok'])
  })

  test('skips non-data lines (comments)', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode(': this is a comment\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"content","text":"hello"}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-6"}\n\n'))
            controller.close()
          },
        })
        return new HttpResponse(stream, {
          headers: { 'Content-Type': 'text/event-stream' },
        })
      })
    )

    const chunks = []
    token.set('key')

    await api.streamChat(
      'default', '', 'hello',
      (chunk) => chunks.push(chunk),
      () => {},
      () => {},
    )

    expect(chunks).toEqual(['hello'])
  })

  test('401 during stream clears token and throws', async () => {
    server.use(
      http.post('/api/v1/chat', () => new HttpResponse(null, { status: 401 }))
    )

    token.set('old-key')
    await expect(
      api.streamChat('default', '', 'hello', () => {}, () => {}, () => {})
    ).rejects.toThrow('Unauthorized')

    const { get } = await import('svelte/store')
    expect(get(token)).toBe('')
  })

  test('thinking and tool_approval events are forwarded to onToolEvent', async () => {
    server.use(
      http.post('/api/v1/chat', () => {
        const encoder = new TextEncoder()
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('data: {"type":"thinking","text":"hmm"}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"tool_approval","approval_id":"a1","tool":"t","approval_status":"pending"}\n\n'))
            controller.enqueue(encoder.encode('data: {"type":"done","session_id":"sess-7"}\n\n'))
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
      'default', '', 'hello',
      () => {},
      () => {},
      (evt) => toolEvents.push(evt),
    )

    expect(toolEvents).toHaveLength(2)
    expect(toolEvents[0].type).toBe('thinking')
    expect(toolEvents[1].type).toBe('tool_approval')
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

  test('models() returns empty array on error', async () => {
    server.use(
      http.get('/api/v1/models', () =>
        HttpResponse.json({ error: 'fail' }, { status: 500 })
      )
    )
    token.set('key')
    const result = await api.models()
    expect(result).toEqual([])
  })

  test('modelDetails() returns model details', async () => {
    token.set('key')
    const result = await api.modelDetails()
    expect(result).toHaveLength(4)
    expect(result[0].provider).toBe('openrouter')
  })

  test('modelDetails() with provider filter sends query param', async () => {
    let capturedUrl
    server.use(
      http.get('/api/v1/models/details', ({ request }) => {
        capturedUrl = request.url
        return HttpResponse.json({ models: [] })
      })
    )
    token.set('key')
    await api.modelDetails('anthropic')
    expect(capturedUrl).toContain('provider=anthropic')
  })

  test('costs() returns cost data', async () => {
    token.set('key')
    const result = await api.costs()
    expect(result.global_cost).toBe(1.25)
  })

  test('costs() with agent filter sends query param', async () => {
    let capturedUrl
    server.use(
      http.get('/api/v1/costs', ({ request }) => {
        capturedUrl = request.url
        return HttpResponse.json({ global_cost: 0 })
      })
    )
    token.set('key')
    await api.costs('default')
    expect(capturedUrl).toContain('agent=default')
  })

  test('skills() returns skill list', async () => {
    token.set('key')
    const result = await api.skills()
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('greeting')
  })

  test('schedules() returns schedule list', async () => {
    token.set('key')
    const result = await api.schedules()
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('daily-check')
  })

  test('sessions() returns paginated session list', async () => {
    token.set('key')
    const result = await api.sessions()
    expect(result.sessions).toHaveLength(2)
    expect(result.total).toBe(2)
  })

  test('listTools() returns tool list', async () => {
    token.set('key')
    const result = await api.listTools()
    expect(result.tools || result).toBeTruthy()
  })

  test('listKeys() returns key list', async () => {
    token.set('key')
    const result = await api.listKeys()
    expect(result).toHaveLength(1)
    expect(result[0].name).toBe('test-key')
  })

  test('createKey() creates a key', async () => {
    token.set('key')
    const result = await api.createKey('new-key', ['chat'])
    expect(result.key).toBe('dk_newkey123')
  })

  test('rotateKey() returns new key', async () => {
    token.set('key')
    const result = await api.rotateKey('key-1')
    expect(result.key).toBe('dk_rotated123')
  })

  test('health() returns status', async () => {
    const result = await api.health()
    expect(result.status).toBe('ok')
  })

  test('kvList() with prefix sends query param', async () => {
    let capturedUrl
    server.use(
      http.get('/api/v1/kv/:agent', ({ request }) => {
        capturedUrl = request.url
        return HttpResponse.json([])
      })
    )
    token.set('key')
    await api.kvList('default', 'user:')
    expect(capturedUrl).toContain('prefix=user%3A')
  })

  test('approvals() with status filter sends query param', async () => {
    let capturedUrl
    server.use(
      http.get('/api/v1/approvals', ({ request }) => {
        capturedUrl = request.url
        return HttpResponse.json([])
      })
    )
    token.set('key')
    await api.approvals('pending')
    expect(capturedUrl).toContain('status=pending')
  })

  test('updateAgentConfig() sends PATCH', async () => {
    let capturedMethod, capturedBody
    server.use(
      http.patch('/api/v1/agents/:name', async ({ request }) => {
        capturedMethod = request.method
        capturedBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )
    token.set('key')
    await api.updateAgentConfig('default', { llm_model: 'gpt-4o' })
    expect(capturedMethod).toBe('PATCH')
    expect(capturedBody.llm_model).toBe('gpt-4o')
  })

  test('llmProviders() returns provider data', async () => {
    token.set('key')
    const result = await api.llmProviders()
    expect(result.default_provider).toBe('openrouter')
    expect(result.providers).toHaveLength(4)
  })

  test('authStatus() returns auth config', async () => {
    token.set('key')
    const result = await api.authStatus()
    expect(result.password_enabled).toBe(true)
  })

  test('serverConfig() returns server config', async () => {
    token.set('key')
    const result = await api.serverConfig()
    expect(result.listen).toBe(':8080')
  })

  test('passwordLogin() succeeds with correct password', async () => {
    const result = await api.passwordLogin('correct')
    expect(result.token).toBe('session-token-123')
  })

  test('passwordLogin() throws on invalid password', async () => {
    await expect(api.passwordLogin('wrong')).rejects.toThrow('Invalid password')
  })

  test('rawPasswordLogin() returns raw Response', async () => {
    const result = await api.rawPasswordLogin('correct')
    expect(result).toBeInstanceOf(Response)
    expect(result.ok).toBe(true)
  })

  test('logout() calls /auth/logout', async () => {
    const result = await api.logout()
    expect(result.ok).toBe(true)
  })

  test('sessionCheck() returns session status', async () => {
    const result = await api.sessionCheck()
    expect(result.authenticated).toBe(false)
  })

  test('authConfig() returns auth config', async () => {
    const result = await api.authConfig()
    expect(result.mode).toBe('token')
  })

  test('setupStatus() returns setup status', async () => {
    const result = await api.setupStatus()
    expect(result.needs_setup).toBe(false)
  })

  test('setupInit() creates initial key', async () => {
    const result = await api.setupInit('my-key', ['chat', 'admin'])
    expect(result.key).toBe('dk_setup123')
  })

  test('setupAccount() creates account', async () => {
    const result = await api.setupAccount('1234', 'mypassword')
    expect(result.ok).toBe(true)
  })

  test('deleteSkill() returns null on 204', async () => {
    token.set('key')
    const result = await api.deleteSkill('default', 'greeting')
    expect(result).toBeNull()
  })

  test('deleteSchedule() returns null on 204', async () => {
    token.set('key')
    const result = await api.deleteSchedule('daily-check')
    expect(result).toBeNull()
  })

  test('removeTool() returns null on 204', async () => {
    token.set('key')
    const result = await api.removeTool('web_search')
    expect(result).toBeNull()
  })

  test('deleteKey() returns null on 204', async () => {
    token.set('key')
    const result = await api.deleteKey('key-1')
    expect(result).toBeNull()
  })

  test('revokeKey() returns null on 204', async () => {
    token.set('key')
    const result = await api.revokeKey('key-1')
    expect(result).toBeNull()
  })

  test('addSkill() sends POST', async () => {
    let capturedBody
    server.use(
      http.post('/api/v1/skills/:agent', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )
    token.set('key')
    await api.addSkill('default', { name: 'test', content: 'body' })
    expect(capturedBody.name).toBe('test')
  })

  test('updateSkill() sends PUT', async () => {
    let capturedMethod
    server.use(
      http.put('/api/v1/skills/:agent/:name', ({ request }) => {
        capturedMethod = request.method
        return HttpResponse.json({ ok: true })
      })
    )
    token.set('key')
    await api.updateSkill('default', 'greeting', { content: 'updated' })
    expect(capturedMethod).toBe('PUT')
  })

  test('addSchedule() sends POST', async () => {
    let capturedBody
    server.use(
      http.post('/api/v1/schedules', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )
    token.set('key')
    await api.addSchedule({ name: 'test', cron: '0 * * * *' })
    expect(capturedBody.name).toBe('test')
  })

  test('changePassword() sends current and new password', async () => {
    let capturedBody
    server.use(
      http.post('/api/v1/auth/password', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )
    token.set('key')
    await api.changePassword('correct', 'newpass12345')
    expect(capturedBody.current_password).toBe('correct')
    expect(capturedBody.new_password).toBe('newpass12345')
  })

  test('onboarding() returns onboarding status', async () => {
    token.set('key')
    const result = await api.onboarding()
    expect(result.show_onboarding).toBe(false)
  })

  test('updateLLMProvider() sends PATCH', async () => {
    let capturedBody
    server.use(
      http.patch('/api/v1/llm/providers/:name', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ ok: true })
      })
    )
    token.set('key')
    await api.updateLLMProvider('openrouter', { api_key: 'newkey' })
    expect(capturedBody.api_key).toBe('newkey')
  })

  test('restartTool() sends POST', async () => {
    let called = false
    server.use(
      http.post('/api/v1/tools/:name/restart', () => {
        called = true
        return HttpResponse.json({ ok: true })
      })
    )
    token.set('key')
    await api.restartTool('web_search')
    expect(called).toBe(true)
  })

  test('toolHealth() returns health status', async () => {
    token.set('key')
    const result = await api.toolHealth('web_search')
    expect(result.status).toBe('connected')
  })

  test('browserProfiles() returns profiles', async () => {
    token.set('key')
    const result = await api.browserProfiles()
    expect(result).toHaveLength(1)
  })

  test('browserSessions() returns sessions', async () => {
    token.set('key')
    const result = await api.browserSessions()
    expect(result).toHaveLength(1)
  })

  test('listAutoApprove() returns rules', async () => {
    token.set('key')
    const result = await api.listAutoApprove()
    expect(result).toHaveLength(1)
  })

  test('listAutoApprove() with agent filter sends query param', async () => {
    let capturedUrl
    server.use(
      http.get('/api/v1/auto-approve', ({ request }) => {
        capturedUrl = request.url
        return HttpResponse.json([])
      })
    )
    token.set('key')
    await api.listAutoApprove('default')
    expect(capturedUrl).toContain('agent=default')
  })
})
