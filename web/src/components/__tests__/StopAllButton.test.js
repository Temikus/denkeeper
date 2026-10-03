import { describe, test, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { api } from '../../api.js'
import StopAllButton, { HOLD_MS } from '../StopAllButton.svelte'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

const button = () => screen.getByTestId('stop-all')

describe('StopAllButton', () => {
  test('letting go early stops nothing and explains the hold', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.pointerDown(button(), { button: 0 })
    expect(button()).toHaveTextContent('Keep holding…')
    vi.advanceTimersByTime(HOLD_MS - 100)
    await fireEvent.pointerUp(button())
    vi.advanceTimersByTime(HOLD_MS)

    expect(panic).not.toHaveBeenCalled()
    expect(screen.getByRole('status')).toHaveTextContent('Hold to stop all agents')
  })

  test('a full hold stops everything once', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.pointerDown(button(), { button: 0 })
    await vi.advanceTimersByTimeAsync(HOLD_MS)
    await fireEvent.pointerUp(button())

    expect(panic).toHaveBeenCalledTimes(1)
  })

  test('dragging off the button cancels the hold', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.pointerDown(button(), { button: 0 })
    await fireEvent.pointerLeave(button())
    await vi.advanceTimersByTimeAsync(HOLD_MS * 2)

    expect(panic).not.toHaveBeenCalled()
  })

  test('a right-click hold does nothing', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.pointerDown(button(), { button: 2 })
    await vi.advanceTimersByTimeAsync(HOLD_MS * 2)

    expect(panic).not.toHaveBeenCalled()
  })

  test('holding Space works, and key auto-repeat does not restart the timer', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.keyDown(button(), { key: ' ' })
    await vi.advanceTimersByTimeAsync(HOLD_MS / 2)
    await fireEvent.keyDown(button(), { key: ' ', repeat: true })
    await vi.advanceTimersByTimeAsync(HOLD_MS / 2)

    expect(panic).toHaveBeenCalledTimes(1)
  })

  test('releasing Enter early cancels', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.keyDown(button(), { key: 'Enter' })
    await fireEvent.keyUp(button(), { key: 'Enter' })
    await vi.advanceTimersByTimeAsync(HOLD_MS * 2)

    expect(panic).not.toHaveBeenCalled()
  })

  test('a failed stop surfaces inline and re-enables the button', async () => {
    vi.spyOn(api, 'panic').mockRejectedValue(new Error('nope'))
    render(StopAllButton)

    await fireEvent.pointerDown(button(), { button: 0 })
    await vi.advanceTimersByTimeAsync(HOLD_MS)

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Stop failed: nope'))
    expect(button()).not.toBeDisabled()
  })

  // VoiceOver and NVDA browse mode send a click with detail 0 and no
  // pointer or key events, so the hold can't happen.
  test('a screen-reader click arms, and a second one within 3 s stops', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.click(button(), { detail: 0 })
    expect(panic).not.toHaveBeenCalled()
    expect(screen.getByRole('status')).toHaveTextContent('Press again to stop all agents')

    vi.advanceTimersByTime(1000)
    await fireEvent.click(button(), { detail: 0 })
    expect(panic).toHaveBeenCalledTimes(1)
  })

  test('a second screen-reader click after the window only re-arms', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.click(button(), { detail: 0 })
    vi.advanceTimersByTime(3500)
    await fireEvent.click(button(), { detail: 0 })

    expect(panic).not.toHaveBeenCalled()
  })

  test('an ordinary mouse click does nothing', async () => {
    const panic = vi.spyOn(api, 'panic').mockResolvedValue(null)
    render(StopAllButton)

    await fireEvent.click(button(), { detail: 1 })
    await fireEvent.click(button(), { detail: 1 })

    expect(panic).not.toHaveBeenCalled()
  })

  test('the button is disabled while the request is in flight', async () => {
    let release
    vi.spyOn(api, 'panic').mockReturnValue(new Promise((r) => { release = r }))
    render(StopAllButton)

    await fireEvent.pointerDown(button(), { button: 0 })
    await vi.advanceTimersByTimeAsync(HOLD_MS)
    expect(button()).toBeDisabled()
    expect(button()).toHaveTextContent('Stopping…')

    release(null)
    await waitFor(() => expect(button()).not.toBeDisabled())
  })
})
