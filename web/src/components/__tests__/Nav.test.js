import { describe, test, expect, afterEach } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import { get } from 'svelte/store'
import { attention } from '../../attention.js'
import { setup, wizardOpen } from '../../setupStore.js'
import Nav from '../Nav.svelte'

afterEach(() => attention.set({ pendingApprovals: 0, unhealthyTools: [] }))

const toggle = (getByRole, name) => getByRole('button', { name: new RegExp(`^${name}`) })

describe('Nav', () => {
  test('highlights the active route link', () => {
    const { container } = render(Nav, { props: { active: 'chat' } })
    const activeLink = container.querySelector('.nav-item.active')
    expect(activeLink).not.toBeNull()
    expect(activeLink).toHaveTextContent('Chat')
    expect(activeLink).toHaveAttribute('aria-current', 'page')
  })

  test('renders all navigation links with every group open by default', () => {
    const { container } = render(Nav, { props: { active: 'overview' } })
    expect(container.querySelector('.section-items[hidden]')).toBeNull()
    const links = container.querySelectorAll('.nav-item')
    // 2 top links (overview, chat) + 7 agents section + 4 platform section + 6 admin section = 19
    expect(links).toHaveLength(19)
  })

  test('has a theme toggle button', () => {
    const { getByLabelText } = render(Nav, { props: { active: 'overview' } })
    expect(getByLabelText('Toggle theme')).toBeInTheDocument()
  })

  test('has a logout button and no panic button', () => {
    const { getByText, queryByTestId } = render(Nav, { props: { active: 'overview' } })
    expect(getByText('Logout')).toBeInTheDocument()
    expect(queryByTestId('nav-panic')).toBeNull()
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

  test('closing a group hides its links and shows how many it holds', async () => {
    const { getByRole, queryByText } = render(Nav, { props: { active: 'overview' } })
    const admin = toggle(getByRole, 'Admin')
    expect(admin).toHaveAttribute('aria-expanded', 'true')

    await fireEvent.click(admin)

    expect(admin).toHaveAttribute('aria-expanded', 'false')
    expect(admin).toHaveTextContent('Admin · 6')
    expect(queryByText('API Keys')).not.toBeVisible()
  })

  test('a closed group stays closed after a reload', async () => {
    const first = render(Nav, { props: { active: 'overview' } })
    await fireEvent.click(toggle(first.getByRole, 'Platform'))
    first.unmount()

    const { getByRole, queryByText } = render(Nav, { props: { active: 'overview' } })
    expect(toggle(getByRole, 'Platform')).toHaveAttribute('aria-expanded', 'false')
    expect(queryByText('KV Store')).not.toBeVisible()
  })

  test('arriving on a page inside a closed group opens it', async () => {
    localStorage.setItem('dk_nav_groups', JSON.stringify(['admin']))
    const { getByRole, getByText } = render(Nav, { props: { active: 'costs' } })
    expect(toggle(getByRole, 'Admin')).toHaveAttribute('aria-expanded', 'true')
    expect(getByText('Costs').closest('a')).toHaveClass('active')
  })

  test('unreadable saved state falls back to everything open', () => {
    localStorage.setItem('dk_nav_groups', '{not json')
    const { container } = render(Nav, { props: { active: 'overview' } })
    expect(container.querySelector('.section-items[hidden]')).toBeNull()
  })

  test('the Approvals link counts pending approvals', () => {
    attention.set({ pendingApprovals: 2, unhealthyTools: [] })
    const { getByText } = render(Nav, { props: { active: 'overview' } })
    const link = getByText('Approvals').closest('a')
    expect(link).toHaveTextContent('2')
    expect(link).toHaveTextContent('2 pending')
  })

  test('a closed group still shows the pending count inside it', async () => {
    attention.set({ pendingApprovals: 2, unhealthyTools: [] })
    const { getByRole } = render(Nav, { props: { active: 'overview' } })
    const agents = toggle(getByRole, 'Agents')
    await fireEvent.click(agents)
    expect(agents).toHaveTextContent('(2 pending)')
  })

  test('Platform flags a broken tool server', () => {
    attention.set({ pendingApprovals: 0, unhealthyTools: ['github'] })
    const { getByRole } = render(Nav, { props: { active: 'overview' } })
    expect(toggle(getByRole, 'Platform')).toHaveTextContent('a tool server is unhealthy')
  })
})
