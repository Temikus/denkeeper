import { describe, test, expect, afterEach } from 'vitest'
import { render } from '@testing-library/svelte'
import { attention } from '../../attention.js'
import BottomNav from '../BottomNav.svelte'

afterEach(() => attention.set({ pendingApprovals: 0, unhealthyTools: [] }))

describe('BottomNav', () => {
  test('renders all five tabs, with Approvals in place of Tools', () => {
    const { getByText, queryByText } = render(BottomNav, { props: { active: 'chat' } })
    expect(getByText('Overview')).toBeInTheDocument()
    expect(getByText('Chat')).toBeInTheDocument()
    expect(getByText('Approvals')).toBeInTheDocument()
    expect(getByText('Agents')).toBeInTheDocument()
    expect(getByText('More')).toBeInTheDocument()
    expect(queryByText('Tools')).toBeNull()
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

  test('the Approvals tab shows a badge only while something is pending', async () => {
    const { getByText } = render(BottomNav, { props: { active: '' } })
    const tab = getByText('Approvals').closest('button')
    expect(tab.querySelector('.badge')).toBeNull()

    attention.set({ pendingApprovals: 3, unhealthyTools: [] })
    await Promise.resolve()

    expect(tab.querySelector('.badge')).toHaveTextContent('3')
    expect(tab).toHaveTextContent('3 pending')
  })
})
