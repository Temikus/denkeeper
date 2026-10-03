import { describe, test, expect, afterEach } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import MoreMenu from '../../pages/MoreMenu.svelte'
import { attention } from '../../attention.js'
import { get } from 'svelte/store'
import { theme } from '../../store.js'

afterEach(() => {
  attention.set({ pendingApprovals: 0, unhealthyTools: [] })
  if (get(theme) === 'dark') theme.toggle()
})

describe('MoreMenu', () => {
  test('renders all section headings', () => {
    const { getByText } = render(MoreMenu)
    expect(getByText('Agents')).toBeInTheDocument()
    expect(getByText('Platform')).toBeInTheDocument()
    expect(getByText('Admin')).toBeInTheDocument()
  })

  test('lists the Agents pages that have no tab of their own', () => {
    const { getByText, queryByRole } = render(MoreMenu)
    expect(getByText('Sessions')).toBeInTheDocument()
    expect(getByText('Channels')).toBeInTheDocument()
    expect(getByText('Turn inspector')).toBeInTheDocument()
    expect(queryByRole('button', { name: 'Approvals' })).toBeNull()
  })

  test('Platform and Admin open in place, and Tools is reachable', async () => {
    const { getByRole, queryByRole } = render(MoreMenu)
    const platform = getByRole('button', { name: /^Platform/ })
    expect(platform).toHaveAttribute('aria-expanded', 'false')
    expect(queryByRole('button', { name: 'Tools' })).toBeNull()

    await fireEvent.click(platform)

    expect(platform).toHaveAttribute('aria-expanded', 'true')
    expect(getByRole('button', { name: 'Tools' })).toBeInTheDocument()
    expect(getByRole('button', { name: 'Skills' })).toBeInTheDocument()
  })

  test('the Platform row says when a tool server is down', () => {
    attention.set({ pendingApprovals: 0, unhealthyTools: ['github', 'jira'] })
    const { getByRole } = render(MoreMenu)
    expect(getByRole('button', { name: /^Platform/ })).toHaveTextContent('2 tools down')
  })

  test('renders the theme switch and Logout, and no panic action', () => {
    const { getByText, getByRole, queryByText } = render(MoreMenu)
    expect(getByText('Theme')).toBeInTheDocument()
    expect(getByRole('button', { name: 'Light' })).toHaveAttribute('aria-pressed', 'true')
    expect(getByText('Logout')).toBeInTheDocument()
    expect(queryByText('Panic')).toBeNull()
  })

  test('choosing Dark switches the theme', async () => {
    const { getByRole } = render(MoreMenu)
    await fireEvent.click(getByRole('button', { name: 'Dark' }))
    expect(getByRole('button', { name: 'Dark' })).toHaveAttribute('aria-pressed', 'true')
  })
})
