import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { panicStatus } from '../../wsStore.js'
import { api } from '../../api.js'
import PanicBar from '../PanicBar.svelte'

beforeEach(() => {
  panicStatus.set({ active: false, message: '', since: '' })
})

afterEach(() => {
  vi.restoreAllMocks()
  panicStatus.set({ active: false, message: '', since: '' })
})

describe('PanicBar', () => {
  test('renders nothing while the system is running', () => {
    const { container } = render(PanicBar)
    expect(container.querySelector('.panic-bar')).toBeNull()
  })

  test('appears when panic is active', () => {
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(PanicBar)
    const bar = screen.getByTestId('global-panic-bar')
    expect(bar).toBeInTheDocument()
    expect(bar).toHaveAttribute('role', 'alert')
    expect(bar).toHaveTextContent('All processing paused')
  })

  test('dates the pause from the store timestamp', () => {
    panicStatus.set({
      active: true,
      message: 'paused',
      since: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
    })
    render(PanicBar)
    expect(screen.getByTestId('global-panic-bar')).toHaveTextContent('5m ago')
  })

  // The component mounts at app boot and only re-reads the clock every 30s, so
  // a panic arriving between ticks used to be compared against a stale `now`
  // and render as a countdown ("in 6s") to something that already happened.
  test('a panic arriving after mount reads as elapsed, not as a countdown', async () => {
    render(PanicBar)

    panicStatus.set({
      active: true,
      message: 'paused',
      since: new Date(Date.now() + 5000).toISOString(),
    })

    await waitFor(() => expect(screen.getByTestId('global-panic-bar')).toBeInTheDocument())
    expect(screen.getByTestId('global-panic-bar')).not.toHaveTextContent(/\bin \d/)
  })

  test('omits the timestamp when the server reported none', () => {
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(PanicBar)
    expect(screen.getByTestId('global-panic-bar')).not.toHaveTextContent('ago')
  })

  test('Resume calls the API and the bar clears when the store does', async () => {
    const resume = vi.spyOn(api, 'resume').mockResolvedValue(null)
    panicStatus.set({ active: true, message: 'paused', since: '' })
    const { container } = render(PanicBar)

    await fireEvent.click(screen.getByTestId('global-panic-resume'))
    expect(resume).toHaveBeenCalled()

    // The server broadcasts the resume; the bar follows the store, not the click.
    panicStatus.set({ active: false, message: '', since: '' })
    await waitFor(() => expect(container.querySelector('.panic-bar')).toBeNull())
  })

  test('a failed resume surfaces inline and leaves the bar up', async () => {
    vi.spyOn(api, 'resume').mockRejectedValue(new Error('nope'))
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(PanicBar)

    await fireEvent.click(screen.getByTestId('global-panic-resume'))

    await waitFor(() => expect(screen.getByText(/Resume failed: nope/)).toBeInTheDocument())
    expect(screen.getByTestId('global-panic-bar')).toBeInTheDocument()
  })

  test('the button is disabled while the request is in flight', async () => {
    let release
    vi.spyOn(api, 'resume').mockReturnValue(new Promise((r) => { release = r }))
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(PanicBar)

    const btn = screen.getByTestId('global-panic-resume')
    await fireEvent.click(btn)
    expect(btn).toBeDisabled()

    release(null)
    await waitFor(() => expect(btn).not.toBeDisabled())
  })
})
