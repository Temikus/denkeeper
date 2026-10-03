import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { panicStatus, wsStatus } from '../../wsStore.js'
import { attention } from '../../attention.js'
import { api } from '../../api.js'
import TopBar from '../TopBar.svelte'

function reset() {
  panicStatus.set({ active: false, message: '', since: '' })
  attention.set({ pendingApprovals: 0, unhealthyTools: [] })
  wsStatus.set('connected')
}

beforeEach(reset)

afterEach(() => {
  vi.restoreAllMocks()
  reset()
})

describe('TopBar while running', () => {
  test('shows the breadcrumb for a page inside a section', () => {
    render(TopBar, { props: { active: 'sessions' } })
    const crumb = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(crumb).toHaveTextContent(/^Agents\s*\/\s*Sessions$/)
    expect(screen.getByText('Sessions')).toHaveAttribute('aria-current', 'page')
  })

  test('a top-level page has no section in its breadcrumb', () => {
    render(TopBar, { props: { active: '' } })
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(/^Overview$/)
  })

  test('hides the approvals chip when nothing is pending', () => {
    render(TopBar, { props: { active: 'overview' } })
    expect(screen.queryByTestId('approvals-chip')).toBeNull()
  })

  test('the approvals chip counts and links to the queue', async () => {
    render(TopBar, { props: { active: 'overview' } })
    attention.set({ pendingApprovals: 2, unhealthyTools: [] })
    const chip = await screen.findByTestId('approvals-chip')
    expect(chip).toHaveTextContent('2 approvals')
    expect(chip).toHaveAttribute('href', '#/approvals')
  })

  // .health is the desktop line; phones get the same text beside the brand dot.
  test('reports healthy when connected and every tool is up', () => {
    const { container } = render(TopBar, { props: { active: 'overview' } })
    expect(container.querySelector('.health')).toHaveTextContent('Live · healthy')
    expect(container.querySelector('.mobile-health')).toHaveTextContent('Live · healthy')
  })

  test('a broken tool server turns the health line into a link to Tools', () => {
    attention.set({ pendingApprovals: 0, unhealthyTools: ['github'] })
    const { container } = render(TopBar, { props: { active: 'overview' } })
    const link = container.querySelector('.health')
    expect(link).toHaveTextContent('Live · 1 tool unhealthy')
    expect(link).toHaveAttribute('href', '#/tools')
  })

  test('a dropped socket reads as reconnecting, not live', () => {
    wsStatus.set('reconnecting')
    const { container } = render(TopBar, { props: { active: 'overview' } })
    expect(container.querySelector('.health')).toHaveTextContent('Reconnecting…')
    expect(screen.queryByText(/Live/)).toBeNull()
  })

  test('offers Stop all', () => {
    render(TopBar, { props: { active: 'overview' } })
    expect(screen.getByTestId('stop-all')).toHaveTextContent('Stop all')
    expect(screen.queryByTestId('global-panic-bar')).toBeNull()
  })
})

describe('TopBar while stopped', () => {
  test('turns into the stopped bar', () => {
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(TopBar, { props: { active: 'overview' } })
    const bar = screen.getByTestId('global-panic-bar')
    expect(bar).toHaveTextContent('All agents stopped')
    // Only the message is announced, not the Resume button with it.
    expect(screen.getByRole('alert')).toHaveTextContent('All agents stopped')
    expect(screen.getByRole('alert')).not.toHaveTextContent('Resume')
    expect(screen.queryByTestId('stop-all')).toBeNull()
  })

  test('dates the stop from the store timestamp', () => {
    panicStatus.set({
      active: true,
      message: 'paused',
      since: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
    })
    render(TopBar)
    expect(screen.getByTestId('global-panic-bar')).toHaveTextContent('5m ago')
  })

  // The bar mounts at app boot and only re-reads the clock every 30s, so a
  // panic arriving between ticks used to be compared against a stale `now`
  // and render as a countdown ("in 6s") to something that already happened.
  test('a stop arriving after mount reads as elapsed, not as a countdown', async () => {
    render(TopBar)

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
    render(TopBar)
    expect(screen.getByTestId('global-panic-bar')).not.toHaveTextContent('ago')
  })

  test('says what happens to new messages while stopped', () => {
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(TopBar)
    expect(screen.getByTestId('global-panic-bar')).toHaveTextContent('New messages get a “paused” reply')
  })

  test('Resume calls the API and the bar clears when the store does', async () => {
    const resume = vi.spyOn(api, 'resume').mockResolvedValue(null)
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(TopBar)

    await fireEvent.click(screen.getByTestId('global-panic-resume'))
    expect(resume).toHaveBeenCalled()

    // The server broadcasts the resume; the bar follows the store, not the click.
    panicStatus.set({ active: false, message: '', since: '' })
    await waitFor(() => expect(screen.queryByTestId('global-panic-bar')).toBeNull())
    expect(screen.getByTestId('stop-all')).toBeInTheDocument()
  })

  test('focus follows from Stop all to Resume when the stop came from this tab', async () => {
    render(TopBar)
    screen.getByTestId('stop-all').focus()

    panicStatus.set({ active: true, message: 'paused', since: '' })

    await waitFor(() => expect(screen.getByTestId('global-panic-resume')).toHaveFocus())
  })

  test('a stop from elsewhere does not move focus into the bar', async () => {
    const outside = document.createElement('input')
    document.body.appendChild(outside)
    outside.focus()
    render(TopBar)

    panicStatus.set({ active: true, message: 'paused', since: '' })
    await waitFor(() => expect(screen.getByTestId('global-panic-bar')).toBeInTheDocument())
    await new Promise(r => setTimeout(r, 0))

    expect(outside).toHaveFocus()
    outside.remove()
  })

  test('a failed resume surfaces inline and leaves the bar up', async () => {
    vi.spyOn(api, 'resume').mockRejectedValue(new Error('nope'))
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(TopBar)

    await fireEvent.click(screen.getByTestId('global-panic-resume'))

    await waitFor(() => expect(screen.getByText(/Resume failed: nope/)).toBeInTheDocument())
    expect(screen.getByTestId('global-panic-bar')).toBeInTheDocument()
  })

  test('the button is disabled while the request is in flight', async () => {
    let release
    vi.spyOn(api, 'resume').mockReturnValue(new Promise((r) => { release = r }))
    panicStatus.set({ active: true, message: 'paused', since: '' })
    render(TopBar)

    const btn = screen.getByTestId('global-panic-resume')
    await fireEvent.click(btn)
    expect(btn).toBeDisabled()

    release(null)
    await waitFor(() => expect(btn).not.toBeDisabled())
  })
})
