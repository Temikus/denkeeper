import { describe, test, expect } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import WizardRail from '../wizard/WizardRail.svelte'
import PreviewCard from '../wizard/PreviewCard.svelte'

describe('WizardRail', () => {
  test('marks the current step and shows done summaries', () => {
    render(WizardRail, { props: { steps: [
      { id: 'provider', label: 'Connect a provider', summary: 'anthropic · key works', state: 'done' },
      { id: 'agent', label: 'Create an agent', summary: 'Model and permissions', state: 'current' },
      { id: 'chat', label: 'Connect a chat app', summary: 'Telegram or Discord', state: 'upcoming', optional: true },
    ] } })

    expect(screen.getByTestId('wizard-rail-provider')).toHaveTextContent('anthropic · key works')
    expect(screen.getByTestId('wizard-rail-agent')).toHaveAttribute('aria-current', 'step')
    expect(screen.getByTestId('wizard-rail-chat')).toHaveTextContent('optional')
  })
})

describe('PreviewCard', () => {
  test('is a placeholder before anything is set', () => {
    render(PreviewCard, { props: {} })
    expect(screen.getByTestId('wizard-preview')).toHaveTextContent('Your agent takes shape here')
  })

  test('shows the agent, greeting and chat app', () => {
    render(PreviewCard, { props: {
      name: 'Den', emoji: '🦊', model: 'claude-sonnet-5-5', provider: 'anthropic',
      tier: 'supervised', supervised: true, chatApp: 'Telegram · @my_den_bot', greeting: 'Hi, I\'m Den.',
    } })
    const card = screen.getByTestId('wizard-preview')
    expect(card).toHaveTextContent('Den')
    expect(card).toHaveTextContent('claude-sonnet-5-5 via anthropic')
    expect(card).toHaveTextContent('Supervised')
    expect(card).toHaveTextContent('+ supervisor')
    expect(card).toHaveTextContent('Telegram · @my_den_bot')
    expect(card).toHaveTextContent("Hi, I'm Den.")
  })
})
