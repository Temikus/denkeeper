import { describe, test, expect } from 'vitest'
import { render } from '@testing-library/svelte'
import MoreMenu from '../../pages/MoreMenu.svelte'

describe('MoreMenu', () => {
  test('renders all section headings', () => {
    const { getByText } = render(MoreMenu)
    expect(getByText('Agents')).toBeInTheDocument()
    expect(getByText('Platform')).toBeInTheDocument()
    expect(getByText('Admin')).toBeInTheDocument()
  })

  test('renders navigation items', () => {
    const { getByText } = render(MoreMenu)
    expect(getByText('Sessions')).toBeInTheDocument()
    expect(getByText('Channels')).toBeInTheDocument()
    expect(getByText('Skills')).toBeInTheDocument()
    expect(getByText('Server')).toBeInTheDocument()
    expect(getByText('Settings')).toBeInTheDocument()
  })

  test('renders footer actions', () => {
    const { getByText } = render(MoreMenu)
    expect(getByText('Theme')).toBeInTheDocument()
    expect(getByText('Logout')).toBeInTheDocument()
    expect(getByText('Panic')).toBeInTheDocument()
  })
})
