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
})
