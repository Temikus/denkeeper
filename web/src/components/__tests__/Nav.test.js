import { describe, test, expect } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import { get } from 'svelte/store'
import { setup, wizardOpen } from '../../setupStore.js'
import Nav from '../Nav.svelte'

describe('Nav', () => {
  test('highlights the active route link', () => {
    const { container } = render(Nav, { props: { active: 'chat' } })
    const activeLink = container.querySelector('.nav-item.active')
    expect(activeLink).not.toBeNull()
    expect(activeLink).toHaveTextContent('Chat')
  })

  test('renders all navigation links', () => {
    const { container } = render(Nav, { props: { active: 'overview' } })
    const links = container.querySelectorAll('.nav-item')
    // 2 top links (overview, chat) + 7 agents section + 4 platform section + 6 admin section = 19
    expect(links).toHaveLength(19)
  })

  test('has a theme toggle button', () => {
    const { getByLabelText } = render(Nav, { props: { active: 'overview' } })
    expect(getByLabelText('Toggle theme')).toBeInTheDocument()
  })

  test('has a logout button', () => {
    const { getByText } = render(Nav, { props: { active: 'overview' } })
    expect(getByText('Logout')).toBeInTheDocument()
  })

  test('shows a resume chip after setup was skipped', async () => {
    setup.set({
      loaded: true, completed: true, skipped: true, doneCount: 1, total: 4,
      steps: [{ id: 'provider', done: true }, { id: 'agent', done: false }, { id: 'persona', done: false }, { id: 'chat_app', done: false, optional: true }],
    })
    wizardOpen.set(false)
    const { getByTestId } = render(Nav, { props: { active: 'overview' } })

    const chip = getByTestId('nav-setup-chip')
    expect(chip).toHaveTextContent('Setup · 1 of 4')
    await fireEvent.click(chip)
    expect(get(wizardOpen)).toBe(true)
  })

  test('no setup chip once setup is done', () => {
    setup.set({ loaded: true, completed: true, skipped: false, doneCount: 4, total: 4, steps: [] })
    const { queryByTestId } = render(Nav, { props: { active: 'overview' } })
    expect(queryByTestId('nav-setup-chip')).toBeNull()
  })

  test('overview is highlighted by default', () => {
    const { container } = render(Nav, { props: { active: 'overview' } })
    const activeLink = container.querySelector('.nav-item.active')
    expect(activeLink).toHaveTextContent('Overview')
  })
})
