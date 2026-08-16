import { describe, test, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import KebabMenu from '../KebabMenu.svelte'

function setup(items) {
  const onclick = vi.fn()
  const result = render(KebabMenu, {
    props: { items: items ?? [{ label: 'Do the thing', onclick }] },
  })
  return { ...result, onclick }
}

describe('KebabMenu', () => {
  test('opens the dropdown from the trigger', async () => {
    const { container } = setup()
    const trigger = container.querySelector('.kebab-trigger')
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(container.querySelector('[role="menu"]')).toBeNull()

    await fireEvent.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(container.querySelector('[role="menu"]')).not.toBeNull()
  })

  // The element is what lets a caller that opens a panel from this menu return
  // focus to the control the panel came from, instead of dropping focus to the
  // top of the document when it closes.
  test('hands the trigger element to the item callback', async () => {
    const { container, onclick } = setup()
    const trigger = container.querySelector('.kebab-trigger')

    await fireEvent.click(trigger)
    await fireEvent.click(container.querySelector('.kebab-item'))

    expect(onclick).toHaveBeenCalledTimes(1)
    expect(onclick).toHaveBeenCalledWith(trigger)
  })

  test('a disabled item neither fires nor closes the menu', async () => {
    const onclick = vi.fn()
    const { container } = setup([{ label: 'Nope', onclick, disabled: true }])

    await fireEvent.click(container.querySelector('.kebab-trigger'))
    await fireEvent.click(container.querySelector('.kebab-item'))

    expect(onclick).not.toHaveBeenCalled()
    expect(container.querySelector('[role="menu"]')).not.toBeNull()
  })

  test('Escape closes the menu', async () => {
    const { container } = setup()
    await fireEvent.click(container.querySelector('.kebab-trigger'))
    expect(container.querySelector('[role="menu"]')).not.toBeNull()

    await fireEvent.keyDown(container.querySelector('.kebab-wrap'), { key: 'Escape' })
    expect(container.querySelector('[role="menu"]')).toBeNull()
  })
})
