import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { get } from 'svelte/store'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import { pendingSkillTest } from '../../chatStore.js'
import { wizardOpen } from '../../setupStore.js'

vi.mock('../../router.js', async () => {
  const { writable } = await import('svelte/store')
  return {
    navigate: vi.fn(),
    currentRoute: writable(''),
  }
})

const { navigate } = await import('../../router.js')
const SetupWizard = (await import('../../pages/SetupWizard.svelte')).default

// onboarding builds a GET /onboarding body with the given wizard steps done.
function onboarding(done = {}, extra = {}) {
  const step = (id, detail = {}) => ({ id, done: !!done[id], optional: id === 'chat_app', detail: done[id] ? detail : {} })
  return {
    show_onboarding: true,
    steps: [],
    dismissed: false,
    wizard_completed: false,
    wizard: {
      completed: false,
      skipped: false,
      agent: done.agent ? 'assistant' : '',
      steps: [
        step('provider', { name: 'anthropic', type: 'anthropic' }),
        step('agent', { name: 'assistant', model: 'claude-sonnet-5-5', tier: 'supervised' }),
        step('persona', { display_name: 'Den', emoji: '🦊', theme: 'helpful general-purpose assistant' }),
        step('chat_app', { type: 'telegram' }),
      ],
      done_count: Object.values(done).filter(Boolean).length,
      total: 4,
      restart_required: false,
      restart: { available: true, managed: true },
      ...extra,
    },
  }
}

function serveOnboarding(body) {
  server.use(http.get('/api/v1/onboarding', () => HttpResponse.json(body)))
}

// record captures the JSON body of every request to method+path.
function record(method, path, reply = () => HttpResponse.json({ ok: true })) {
  const bodies = []
  server.use(http[method](path, async ({ request }) => {
    bodies.push(await request.json().catch(() => null))
    return reply()
  }))
  return bodies
}

const continueBtn = () => screen.getByTestId('wizard-continue')

async function headingIs(text) {
  await waitFor(() => expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(text))
}

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  wizardOpen.set(true)
  pendingSkillTest.set(null)
  navigate.mockClear()
  server.use(
    http.get('/api/v1/llm/providers', () => HttpResponse.json({ providers: [], default_provider: '', default_model: '' })),
    http.get('/api/v1/agents', () => HttpResponse.json([])),
  )
  serveOnboarding(onboarding())
})

describe('SetupWizard', () => {
  test('a fresh install starts at the welcome screen', async () => {
    render(SetupWizard)
    await headingIs("Let's set up your first agent")
    expect(screen.getByTestId('wizard-rail-provider')).toHaveTextContent('Where the model runs')

    await fireEvent.click(continueBtn())
    await headingIs('Connect a provider')
  })

  test('pasting a key tests it and Continue saves the provider', async () => {
    const tests = record('post', '/api/v1/llm/providers/test', () => HttpResponse.json({
      status: 'ok', message: 'Key works. 2 models available.', model_count: 2, models: ['claude-haiku-4-5', 'claude-sonnet-5-5'],
    }))
    const creates = record('post', '/api/v1/llm/providers', () => HttpResponse.json({ name: 'anthropic', status: 'created', default: true }, { status: 201 }))

    render(SetupWizard)
    await headingIs("Let's set up")
    await fireEvent.click(continueBtn())
    await headingIs('Connect a provider')

    const key = screen.getByTestId('wizard-provider-apikey')
    await fireEvent.input(key, { target: { value: 'sk-ant-good' } })
    await fireEvent.paste(key)
    await waitFor(() => expect(screen.getByTestId('wizard-provider-status')).toHaveTextContent('Key works'))
    expect(tests[0]).toMatchObject({ type: 'anthropic', api_key: 'sk-ant-good' })

    await fireEvent.click(continueBtn())
    await headingIs('Create an agent')
    expect(creates[0]).toMatchObject({ name: 'anthropic', type: 'anthropic', api_key: 'sk-ant-good' })
    expect(screen.getByTestId('wizard-rail-provider')).toHaveTextContent('anthropic · key works')
  })

  test('a rejected key blocks Continue until Save anyway', async () => {
    record('post', '/api/v1/llm/providers/test', () => HttpResponse.json({ status: 'rejected', message: 'Anthropic says this key is not valid.' }))

    render(SetupWizard)
    await headingIs("Let's set up")
    await fireEvent.click(continueBtn())
    const key = await screen.findByTestId('wizard-provider-apikey')
    await fireEvent.input(key, { target: { value: 'sk-ant-bad' } })
    await fireEvent.blur(key)

    await waitFor(() => expect(screen.getByTestId('wizard-provider-status')).toHaveTextContent('not valid'))
    expect(continueBtn()).toBeDisabled()
    await fireEvent.click(screen.getByTestId('wizard-save-anyway'))
    expect(continueBtn()).not.toBeDisabled()
  })

  test('the agent step preselects models and sends a supervisor', async () => {
    serveOnboarding(onboarding({ provider: true }))
    server.use(http.get('/api/v1/models/details', () => HttpResponse.json({ models: [
      { id: 'claude-haiku-4-5' }, { id: 'claude-sonnet-5-5' },
    ] })))
    const creates = record('post', '/api/v1/agents', () => HttpResponse.json({ name: 'assistant', status: 'created' }, { status: 201 }))

    render(SetupWizard)
    await headingIs('Create an agent')
    await waitFor(() => expect(screen.getByTestId('wizard-agent-model').value).toBe('claude-sonnet-5-5'))
    expect(screen.getByTestId('wizard-supervisor-callout')).toHaveTextContent('supervisor')

    await fireEvent.click(screen.getByTestId('wizard-supervisor-change'))
    expect(screen.getByTestId('wizard-supervisor-model').value).toBe('claude-haiku-4-5')

    await fireEvent.click(continueBtn())
    await headingIs('Give it a personality')
    expect(creates[0]).toMatchObject({
      name: 'assistant',
      llm_provider: 'anthropic',
      llm_model: 'claude-sonnet-5-5',
      session_tier: 'supervised',
      create_supervisor: { name: 'supervisor', llm_model: 'claude-haiku-4-5', timeout: '30s', context_messages: 5 },
    })
  })

  test('autonomous hides the supervisor and sends none', async () => {
    serveOnboarding(onboarding({ provider: true }))
    const creates = record('post', '/api/v1/agents', () => HttpResponse.json({ name: 'assistant', status: 'created' }, { status: 201 }))

    render(SetupWizard)
    await headingIs('Create an agent')
    await fireEvent.click(screen.getByLabelText('Autonomous'))
    expect(screen.queryByTestId('wizard-supervisor-callout')).not.toBeInTheDocument()

    await fireEvent.input(screen.getByTestId('wizard-agent-model'), { target: { value: 'claude-x' } })
    await fireEvent.click(continueBtn())
    await headingIs('Give it a personality')
    expect(creates[0].create_supervisor).toBeUndefined()
  })

  test('an invalid agent name is flagged inline and blocks Continue', async () => {
    serveOnboarding(onboarding({ provider: true }))
    render(SetupWizard)
    await headingIs('Create an agent')

    await fireEvent.input(screen.getByTestId('wizard-agent-name'), { target: { value: 'Bad Name' } })
    expect(screen.getByText('Lowercase letters, numbers and hyphens only.')).toBeInTheDocument()
    expect(continueBtn()).toBeDisabled()
  })

  test('the personality step saves identity through the API, not YAML', async () => {
    serveOnboarding(onboarding({ provider: true, agent: true }))
    const identities = record('put', '/api/v1/agents/:name/identity')
    const souls = record('put', '/api/v1/agents/:name/persona/:section')

    render(SetupWizard)
    await headingIs('Give it a personality')
    expect(screen.getByTestId('wizard-persona-name').value).toBe('Assistant')

    await fireEvent.input(screen.getByTestId('wizard-persona-name'), { target: { value: 'Den' } })
    await fireEvent.click(screen.getByRole('button', { name: '🦊' }))
    await fireEvent.click(screen.getByLabelText(/Concise/))
    expect(screen.getByTestId('wizard-preview')).toHaveTextContent('Den here. What do you need?')

    await fireEvent.click(continueBtn())
    await headingIs('Connect a chat app')
    expect(identities[0]).toEqual({ name: 'Den', emoji: '🦊', theme: 'concise, direct assistant that skips small talk' })
    expect(souls[0].content).toContain('Be genuinely helpful')
  })

  test('Telegram: verify, pair on the first message, confirm, save, restart', async () => {
    serveOnboarding(onboarding({ provider: true, agent: true, persona: true }))
    const saves = record('post', '/api/v1/onboarding/chat-app/save', () => HttpResponse.json({
      status: 'saved', agent: 'assistant', restart_required: true, restart: { available: true, managed: true },
    }))
    const restarts = record('post', '/api/v1/server/restart', () => new HttpResponse(null, { status: 204 }))

    render(SetupWizard)
    await headingIs('Connect a chat app')
    const tokenInput = screen.getByTestId('wizard-chat-token')
    await fireEvent.input(tokenInput, { target: { value: '123456789:AAFabcdefghijklmnopqrstuvwxyz0123456' } })
    await fireEvent.paste(tokenInput)

    await screen.findByText('Connected as @my_den_bot')
    await screen.findByTestId('wizard-pair-sender')
    expect(continueBtn()).toBeDisabled()
    await fireEvent.click(screen.getByTestId('wizard-pair-confirm'))

    // The ready step re-reads progress, which now says a restart is due.
    serveOnboarding(onboarding({ provider: true, agent: true, persona: true, chat_app: true }, { restart_required: true }))
    await fireEvent.click(continueBtn())
    await headingIs('Den is ready')
    expect(saves[0]).toMatchObject({ type: 'telegram', allowed_users: ['4821'], agent: 'assistant', notify_chat_id: '4821' })

    await fireEvent.click(screen.getByTestId('wizard-restart-now'))
    await waitFor(() => expect(restarts.length).toBe(1))
  })

  test('pairing keeps polling on timeout and offers manual entry on conflict', async () => {
    serveOnboarding(onboarding({ provider: true, agent: true, persona: true }))
    let calls = 0
    server.use(http.post('/api/v1/onboarding/chat-app/pair', () => {
      calls++
      return calls === 1
        ? HttpResponse.json({ status: 'timeout', cursor: '5' })
        : HttpResponse.json({ status: 'conflict', message: 'Another program is reading this bot\'s messages.' })
    }))

    render(SetupWizard)
    await headingIs('Connect a chat app')
    const tokenInput = screen.getByTestId('wizard-chat-token')
    await fireEvent.input(tokenInput, { target: { value: '123456789:AAFabcdefghijklmnopqrstuvwxyz0123456' } })
    await fireEvent.paste(tokenInput)

    const manual = await screen.findByTestId('wizard-chat-userid')
    expect(calls).toBe(2)
    expect(screen.getByText(/Another program is reading/)).toBeInTheDocument()
    await fireEvent.input(manual, { target: { value: '777' } })
    expect(continueBtn()).not.toBeDisabled()
  })

  test('skipping the chat app goes to ready, and Open chat prefills nothing and opens chat', async () => {
    serveOnboarding(onboarding({ provider: true, agent: true, persona: true }))
    const completes = record('post', '/api/v1/onboarding/wizard-complete', () => new HttpResponse(null, { status: 204 }))

    render(SetupWizard)
    await headingIs('Connect a chat app')
    await fireEvent.click(screen.getByTestId('wizard-skip-chat'))
    await headingIs('Den is ready')
    expect(screen.queryByTestId('wizard-restart')).not.toBeInTheDocument()

    await fireEvent.click(screen.getByTestId('wizard-open-chat'))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('chat'))
    expect(completes.length).toBe(1)
    expect(get(pendingSkillTest)).toEqual({ agent: 'assistant', command: '', send: false })
    expect(get(wizardOpen)).toBe(false)
  })

  test('a "Try asking" prompt prefills the chat', async () => {
    serveOnboarding(onboarding({ provider: true, agent: true, persona: true, chat_app: true }))
    render(SetupWizard)
    await headingIs('Den is ready')

    await fireEvent.click(screen.getByText('"What can you do for me?"'))
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('chat'))
    expect(get(pendingSkillTest)).toEqual({ agent: 'assistant', command: 'What can you do for me?', send: false })
  })

  test('Set up later confirms inline and records the skip', async () => {
    const skips = record('post', '/api/v1/onboarding/wizard-skip', () => new HttpResponse(null, { status: 204 }))
    render(SetupWizard)
    await headingIs("Let's set up")

    await fireEvent.click(screen.getByTestId('wizard-later'))
    expect(screen.getByTestId('wizard-leave-confirm')).toHaveTextContent('Leave setup for now?')
    await fireEvent.click(screen.getByTestId('wizard-leave'))

    await waitFor(() => expect(get(wizardOpen)).toBe(false))
    expect(skips.length).toBe(1)
  })

  test('a failed skip keeps the wizard open and says why', async () => {
    server.use(http.post('/api/v1/onboarding/wizard-skip', () => HttpResponse.json({ error: 'failed to persist' }, { status: 500 })))
    render(SetupWizard)
    await headingIs("Let's set up")

    await fireEvent.click(screen.getByTestId('wizard-later'))
    await fireEvent.click(screen.getByTestId('wizard-leave'))

    await screen.findByText('failed to persist')
    expect(get(wizardOpen)).toBe(true)
  })

  test('resuming from server progress opens the first unfinished step', async () => {
    serveOnboarding(onboarding({ provider: true, agent: true }))
    render(SetupWizard)
    await headingIs('Give it a personality')
    expect(screen.getByTestId('wizard-rail-provider')).toHaveTextContent('anthropic · saved')
    expect(screen.getByTestId('wizard-rail-agent')).toHaveTextContent('assistant · Supervised')
  })

  test('Continue tests a typed key that was never pasted or blurred', async () => {
    const tests = record('post', '/api/v1/llm/providers/test', () => HttpResponse.json({ status: 'rejected', message: 'Anthropic says this key is not valid.' }))
    const creates = record('post', '/api/v1/llm/providers', () => HttpResponse.json({ name: 'anthropic', status: 'created' }, { status: 201 }))
    render(SetupWizard)
    await headingIs("Let's set up")
    await fireEvent.click(continueBtn())
    await fireEvent.input(await screen.findByTestId('wizard-provider-apikey'), { target: { value: 'sk-ant-typed' } })

    await fireEvent.click(continueBtn())
    await waitFor(() => expect(screen.getByTestId('wizard-provider-status')).toHaveTextContent('not valid'))
    expect(tests.length).toBe(1)
    expect(creates.length).toBe(0)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Connect a provider')
  })

  test('a provider name already in use must be confirmed before its key is replaced', async () => {
    server.use(http.get('/api/v1/llm/providers', () => HttpResponse.json({ providers: [{ name: 'anthropic', type: 'anthropic' }], default_provider: 'anthropic' })))
    render(SetupWizard)
    await headingIs("Let's set up")
    await fireEvent.click(continueBtn())
    const key = await screen.findByTestId('wizard-provider-apikey')
    await fireEvent.input(key, { target: { value: 'sk-ant-good' } })
    await fireEvent.paste(key)
    await waitFor(() => expect(screen.getByTestId('wizard-provider-status')).toHaveTextContent('Key works'))

    expect(screen.getByTestId('wizard-provider-clash')).toHaveTextContent('already exists')
    expect(continueBtn()).toBeDisabled()
    await fireEvent.click(screen.getByTestId('wizard-provider-replace'))
    expect(continueBtn()).not.toBeDisabled()
  })

  test('Enter in the model picker does not submit the step', async () => {
    serveOnboarding(onboarding({ provider: true }))
    const creates = record('post', '/api/v1/agents', () => HttpResponse.json({ name: 'assistant', status: 'created' }, { status: 201 }))
    render(SetupWizard)
    await headingIs('Create an agent')
    const model = screen.getByTestId('wizard-agent-model')
    await fireEvent.input(model, { target: { value: 'claude-x' } })

    await fireEvent.keyDown(model, { key: 'Enter' })
    expect(creates.length).toBe(0)
    await fireEvent.keyDown(screen.getByTestId('wizard-agent-name'), { key: 'Enter' })
    await waitFor(() => expect(creates.length).toBe(1))
  })

  test('stopping an unmanaged server asks first', async () => {
    serveOnboarding(onboarding({ provider: true, agent: true, persona: true, chat_app: true }, {
      restart_required: true, restart: { available: true, managed: false },
    }))
    const restarts = record('post', '/api/v1/server/restart', () => new HttpResponse(null, { status: 204 }))
    render(SetupWizard)
    await headingIs('Den is ready')

    await fireEvent.click(screen.getByText('Stop the server now'))
    expect(restarts.length).toBe(0)
    await fireEvent.click(screen.getByTestId('wizard-stop-confirm'))
    await waitFor(() => expect(restarts.length).toBe(1))
  })

  test('Back returns to the previous step', async () => {
    serveOnboarding(onboarding({ provider: true }))
    render(SetupWizard)
    await headingIs('Create an agent')
    await fireEvent.click(screen.getByRole('button', { name: 'Back' }))
    await headingIs('Connect a provider')
  })
})
