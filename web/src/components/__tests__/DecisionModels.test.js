import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import DecisionModels from '../DecisionModels.svelte'

const openrouter = { name: 'openrouter', type: 'openrouter', enabled: true, serves_decisions: true }
const anthropic = { name: 'anthropic', type: 'anthropic', enabled: true, serves_decisions: false }
const jev = { name: 'jev', provider: 'openrouter', model: 'typesafe/jev-1.13', timeout: '5s', max_input_tokens: 30000, used_by: [] }

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('DecisionModels', () => {
  test('renders a card per decision model with its config', () => {
    render(DecisionModels, { props: { deciders: [jev], providers: [openrouter] } })
    expect(screen.getByText('jev')).toBeInTheDocument()
    expect(screen.getByText('typesafe/jev-1.13')).toBeInTheDocument()
    expect(screen.getByText(/via openrouter · timeout 5s/)).toBeInTheDocument()
  })

  test('add form lists only providers that serve decisions', async () => {
    render(DecisionModels, { props: { providers: [openrouter, anthropic] } })
    await fireEvent.click(screen.getByTestId('add-decider-btn'))
    const options = [...screen.getByTestId('decider-provider-select').options].map(o => o.value)
    expect(options).toEqual(['openrouter'])
  })

  test('without a decision provider the form offers to add OpenRouter', async () => {
    const onAddProvider = vi.fn()
    render(DecisionModels, { props: { providers: [anthropic], onAddProvider } })
    await fireEvent.click(screen.getByTestId('add-decider-btn'))
    await fireEvent.click(screen.getByTestId('add-openrouter-btn'))
    expect(onAddProvider).toHaveBeenCalled()
  })

  test('invalid name shows an inline error and sends nothing', async () => {
    let posted = false
    server.use(http.post('/api/v1/llm/deciders', () => { posted = true; return HttpResponse.json({}, { status: 201 }) }))
    render(DecisionModels, { props: { providers: [openrouter] } })
    await fireEvent.click(screen.getByTestId('add-decider-btn'))
    await fireEvent.input(screen.getByTestId('decider-name-input'), { target: { value: 'Bad Name' } })
    await fireEvent.input(screen.getByTestId('decider-model-input'), { target: { value: 'm' } })
    await fireEvent.click(screen.getByTestId('decider-save-btn'))
    expect(screen.getByRole('alert')).toHaveTextContent('lowercase')
    expect(posted).toBe(false)
  })

  test('add sends the decider and refreshes, omitting empty optional fields', async () => {
    let body
    server.use(http.post('/api/v1/llm/deciders', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ decider: {}, restart_required: false }, { status: 201 })
    }))
    const onChange = vi.fn()
    render(DecisionModels, { props: { providers: [openrouter], onChange } })
    await fireEvent.click(screen.getByTestId('add-decider-btn'))
    await fireEvent.input(screen.getByTestId('decider-name-input'), { target: { value: 'jev' } })
    await fireEvent.input(screen.getByTestId('decider-model-input'), { target: { value: 'typesafe/jev-1.13' } })
    await fireEvent.click(screen.getByTestId('decider-save-btn'))
    await waitFor(() => expect(onChange).toHaveBeenCalled())
    expect(body).toEqual({ name: 'jev', provider: 'openrouter', model: 'typesafe/jev-1.13' })
  })

  test('test first shows latency and cost inline', async () => {
    render(DecisionModels, { props: { providers: [openrouter] } })
    await fireEvent.click(screen.getByTestId('add-decider-btn'))
    await fireEvent.input(screen.getByTestId('decider-model-input'), { target: { value: 'typesafe/jev-1.13' } })
    await fireEvent.click(screen.getByTestId('decider-test-btn'))
    await waitFor(() => expect(screen.getByText(/Test passed · 172 ms/)).toBeInTheDocument())
  })

  test('changing the model hides a test result for the old one', async () => {
    render(DecisionModels, { props: { providers: [openrouter] } })
    await fireEvent.click(screen.getByTestId('add-decider-btn'))
    await fireEvent.input(screen.getByTestId('decider-model-input'), { target: { value: 'typesafe/jev-1.13' } })
    await fireEvent.click(screen.getByTestId('decider-test-btn'))
    await waitFor(() => expect(screen.getByText(/Test passed · 172 ms/)).toBeInTheDocument())
    await fireEvent.input(screen.getByTestId('decider-model-input'), { target: { value: 'typesafe/jev-2' } })
    expect(screen.queryByText(/Test passed/)).not.toBeInTheDocument()
  })

  test('saving an edit clears the card test result', async () => {
    server.use(http.patch('/api/v1/llm/deciders/:name', () => HttpResponse.json({ decider: {}, restart_required: false })))
    render(DecisionModels, { props: { deciders: [jev], providers: [openrouter], onChange: vi.fn() } })
    await fireEvent.click(screen.getByRole('button', { name: 'Test' }))
    await waitFor(() => expect(screen.getByText(/Test passed · 172 ms/)).toBeInTheDocument())
    await fireEvent.click(screen.getByText('Edit'))
    await fireEvent.input(screen.getByTestId('decider-edit-model'), { target: { value: 'typesafe/jev-2' } })
    await fireEvent.click(screen.getByTestId('decider-edit-save'))
    await waitFor(() => expect(screen.queryByTestId('decider-edit-save')).not.toBeInTheDocument())
    expect(screen.queryByText(/Test passed/)).not.toBeInTheDocument()
  })

  test('card tests in flight on two cards each keep their own state', async () => {
    const release = {}
    server.use(http.post('/api/v1/llm/deciders/test', async ({ request }) => {
      const { name } = await request.json()
      await new Promise(resolve => { release[name] = resolve })
      return HttpResponse.json({ status: 'ok', latency_ms: 10, cost_usd: 0 })
    }))
    const other = { ...jev, name: 'jev-b' }
    render(DecisionModels, { props: { deciders: [jev, other], providers: [openrouter] } })
    const [a, b] = screen.getAllByRole('button', { name: 'Test' })
    await fireEvent.click(a)
    await fireEvent.click(b)
    await waitFor(() => expect(release['jev'] && release['jev-b']).toBeTruthy())
    expect(screen.getAllByRole('button', { name: 'Testing…' })).toHaveLength(2)
    release['jev']()
    await waitFor(() => expect(screen.getByText(/Test passed · 10 ms/)).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Testing…' })).toBeDisabled()
    release['jev-b']()
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Test' })).toHaveLength(2))
  })

  test('a used decision model cannot be deleted and links to its users', () => {
    const used = { ...jev, used_by: ['agent:pamela', 'eval.judge_decider'] }
    render(DecisionModels, { props: { deciders: [used], providers: [openrouter] } })
    expect(screen.getByTestId('delete-decider-btn')).toBeDisabled()
    expect(screen.getByText('pamela · supervisor stage').closest('a')).toHaveAttribute('href', '#/agents/pamela?card=permission')
    expect(screen.getByText('Eval judge')).toBeInTheDocument()
  })

  test('an unused decision model deletes after inline confirm', async () => {
    let deleted = ''
    server.use(http.delete('/api/v1/llm/deciders/:name', ({ params }) => { deleted = params.name; return new HttpResponse(null, { status: 204 }) }))
    render(DecisionModels, { props: { deciders: [jev], providers: [openrouter] } })
    await fireEvent.click(screen.getByTestId('delete-decider-btn'))
    await fireEvent.click(screen.getByTestId('decider-delete-confirm-btn'))
    await waitFor(() => expect(deleted).toBe('jev'))
  })

  test('changing the model of a used decision model warns about calibration', async () => {
    const used = { ...jev, used_by: ['agent:pamela'] }
    render(DecisionModels, { props: { deciders: [used], providers: [openrouter] } })
    await fireEvent.click(screen.getByText('Edit'))
    expect(screen.queryByTestId('decider-stale-warning')).not.toBeInTheDocument()
    await fireEvent.input(screen.getByTestId('decider-edit-model'), { target: { value: 'typesafe/jev-2' } })
    expect(screen.getByTestId('decider-stale-warning')).toHaveTextContent('Thresholds for pamela were calibrated on typesafe/jev-1.13')
  })

  test('edit sends empty defaults so the server restores them', async () => {
    let body
    server.use(http.patch('/api/v1/llm/deciders/:name', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ decider: {}, restart_required: false })
    }))
    render(DecisionModels, { props: { deciders: [jev], providers: [openrouter] } })
    await fireEvent.click(screen.getByText('Edit'))
    await fireEvent.click(screen.getByTestId('decider-edit-save'))
    await waitFor(() => expect(body).toEqual({ provider: 'openrouter', model: 'typesafe/jev-1.13', timeout: '', max_input_tokens: 0 }))
  })

  test('deep link opens the form and offers to use the new model for the agent', async () => {
    render(DecisionModels, { props: { providers: [openrouter], openAdd: true, addFor: 'pamela' } })
    await waitFor(() => expect(screen.getByTestId('decider-form')).toBeInTheDocument())
    await fireEvent.input(screen.getByTestId('decider-name-input'), { target: { value: 'jev' } })
    await fireEvent.input(screen.getByTestId('decider-model-input'), { target: { value: 'typesafe/jev-1.13' } })
    await fireEvent.click(screen.getByTestId('decider-save-btn'))
    const link = await screen.findByTestId('use-for-agent')
    expect(link).toHaveAttribute('href', '#/agents/pamela?card=permission&decider=jev')
  })

  test('empty state offers an add button', async () => {
    render(DecisionModels, { props: { providers: [openrouter] } })
    await fireEvent.click(screen.getByTestId('empty-add-decider-btn'))
    expect(screen.getByTestId('decider-form')).toBeInTheDocument()
  })

  test('the delete confirm hides the card actions', async () => {
    render(DecisionModels, { props: { deciders: [jev], providers: [openrouter] } })
    await fireEvent.click(screen.getByTestId('delete-decider-btn'))
    expect(screen.getByTestId('decider-delete-confirm')).toBeInTheDocument()
    expect(screen.queryByText('Edit')).not.toBeInTheDocument()
  })

  test('a provider added while the form is open becomes the selection', async () => {
    const { rerender } = render(DecisionModels, { props: { providers: [anthropic] } })
    await fireEvent.click(screen.getByTestId('add-decider-btn'))
    await rerender({ providers: [anthropic, openrouter] })

    expect(screen.getByTestId('decider-provider-select')).toHaveValue('openrouter')
  })

  test('clearing the token limit in edit sends 0 to restore the default', async () => {
    let body
    server.use(http.patch('/api/v1/llm/deciders/:name', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ decider: {}, restart_required: false })
    }))
    render(DecisionModels, { props: { deciders: [{ ...jev, max_input_tokens: 8000 }], providers: [openrouter] } })
    await fireEvent.click(screen.getByText('Edit'))
    const input = screen.getByDisplayValue('8000')
    await fireEvent.input(input, { target: { value: '' } })
    await fireEvent.click(screen.getByTestId('decider-edit-save'))

    await waitFor(() => expect(body?.max_input_tokens).toBe(0))
  })
})
