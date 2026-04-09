import { describe, test, expect } from 'vitest'
import { render } from '@testing-library/svelte'
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
    // 2 top links (overview, chat) + 4 agents section + 4 platform section + 3 admin section = 13
    expect(links).toHaveLength(13)
  })

  test('has a theme toggle button', () => {
    const { getByLabelText } = render(Nav, { props: { active: 'overview' } })
    expect(getByLabelText('Toggle theme')).toBeInTheDocument()
  })

  test('has a logout button', () => {
    const { getByText } = render(Nav, { props: { active: 'overview' } })
    expect(getByText('Logout')).toBeInTheDocument()
  })

  test('overview is highlighted by default', () => {
    const { container } = render(Nav, { props: { active: 'overview' } })
    const activeLink = container.querySelector('.nav-item.active')
    expect(activeLink).toHaveTextContent('Overview')
  })
})
