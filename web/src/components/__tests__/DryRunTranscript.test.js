import { describe, test, expect } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import DryRunTranscript from '../DryRunTranscript.svelte'

function makeTranscript(overrides = {}) {
  return {
    model: 'kimi-k2.6',
    response: 'Filed three follow-ups.',
    rounds: 3,
    duration_ms: 5000,
    cost_usd: 0.02,
    suppressed_count: 0,
    tool_calls: [],
    ...overrides,
  }
}

describe('DryRunTranscript — stop reason', () => {
  test('a turn the model finished on its own carries no badge', () => {
    render(DryRunTranscript, { props: { transcript: makeTranscript() } })
    expect(screen.queryByTestId('stop-reason')).not.toBeInTheDocument()
  })

  test('each stop reason reads as plain language', () => {
    const cases = {
      max_rounds: 'hit the round limit',
      repeated_calls: 'repeated the same call',
      stop_requested: 'stopped on request',
    }
    for (const [reason, label] of Object.entries(cases)) {
      const { unmount } = render(DryRunTranscript, {
        props: { transcript: makeTranscript({ stop_reason: reason }) },
      })
      expect(screen.getByTestId('stop-reason')).toHaveTextContent(`Cut short: ${label}`)
      unmount()
    }
  })

  test('the badge spells out the wrap-up caveat in its tooltip', () => {
    render(DryRunTranscript, { props: { transcript: makeTranscript({ stop_reason: 'max_rounds' }) } })
    expect(screen.getByTestId('stop-reason'))
      .toHaveAttribute('title', 'The response below is a wrap-up, not a completed answer.')
  })

  test('an unrecognised reason still surfaces, verbatim', () => {
    render(DryRunTranscript, { props: { transcript: makeTranscript({ stop_reason: 'some_new_reason' }) } })
    expect(screen.getByTestId('stop-reason')).toHaveTextContent('some_new_reason')
  })
})

describe('DryRunTranscript — serving upstream', () => {
  test('a routed upstream shows beside the model', () => {
    render(DryRunTranscript, {
      props: { transcript: makeTranscript({ model: 'moonshotai/kimi-k2', upstream: 'deepinfra/fp8' }) },
    })
    const el = screen.getByTestId('upstream')
    expect(el).toHaveTextContent('via deepinfra/fp8')
    expect(el).toHaveAttribute('title', 'The provider that actually served this turn.')
  })

  test('a preview with no upstream shows nothing', () => {
    render(DryRunTranscript, { props: { transcript: makeTranscript() } })
    expect(screen.queryByTestId('upstream')).not.toBeInTheDocument()
  })

  test('an upstream equal to the model name is not repeated', () => {
    render(DryRunTranscript, {
      props: { transcript: makeTranscript({ model: 'kimi-k2.6', upstream: 'kimi-k2.6' }) },
    })
    expect(screen.queryByTestId('upstream')).not.toBeInTheDocument()
  })

  test('the model match ignores case and surrounding space', () => {
    render(DryRunTranscript, {
      props: { transcript: makeTranscript({ model: 'Kimi-K2.6', upstream: ' kimi-k2.6 ' }) },
    })
    expect(screen.queryByTestId('upstream')).not.toBeInTheDocument()
  })

  test('a vendor prefix is not a match — the upstream still shows', () => {
    render(DryRunTranscript, {
      props: { transcript: makeTranscript({ model: 'anthropic/claude-opus-4', upstream: 'bedrock' }) },
    })
    expect(screen.getByTestId('upstream')).toHaveTextContent('via bedrock')
  })
})
