import { describe, test, expect } from 'vitest'
import { render } from '@testing-library/svelte'
import BottomNav from '../BottomNav.svelte'

describe('BottomNav', () => {
  test('renders all five tabs', () => {
    const { getByText } = render(BottomNav, { props: { active: 'chat' } })
    expect(getByText('Overview')).toBeInTheDocument()
    expect(getByText('Chat')).toBeInTheDocument()
    expect(getByText('Agents')).toBeInTheDocument()
    expect(getByText('Tools')).toBeInTheDocument()
    expect(getByText('More')).toBeInTheDocument()
  })

  test('marks active tab with aria-current', () => {
    const { getByText } = render(BottomNav, { props: { active: 'agents' } })
    expect(getByText('Agents').closest('button')).toHaveAttribute('aria-current', 'page')
    expect(getByText('Chat').closest('button')).not.toHaveAttribute('aria-current')
  })

  test('has accessible navigation landmark', () => {
    const { container } = render(BottomNav, { props: { active: '' } })
    expect(container.querySelector('nav[aria-label="Main navigation"]')).toBeInTheDocument()
  })
})
