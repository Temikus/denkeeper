import { describe, test, expect, afterEach } from 'vitest'
import { render } from '@testing-library/svelte'
import MoreMenu from '../../pages/MoreMenu.svelte'
import { panicStatus } from '../../wsStore.js'

afterEach(() => panicStatus.set({ active: false, message: '', since: '' }))

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

  test('offers Resume when the store says the system is panicked', () => {
    panicStatus.set({ active: true, message: 'paused', since: '' })
    const { getByText, queryByText } = render(MoreMenu)
    expect(getByText('Resume')).toBeInTheDocument()
    expect(queryByText('Panic')).not.toBeInTheDocument()
  })

  test('dates the pause when the server reported a panic time', () => {
    panicStatus.set({
      active: true,
      message: 'paused',
      since: new Date(Date.now() - 90 * 60 * 1000).toISOString(),
    })
    const { getByText } = render(MoreMenu)
    expect(getByText('Paused 1h ago')).toBeInTheDocument()
  })
})
