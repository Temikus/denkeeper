import { describe, test, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import FilterChips from '../FilterChips.svelte'

const items = [
  { value: '', label: 'All' },
  { value: 'a', label: 'Alpha', testid: 'chip-alpha' },
  { value: 'b', label: 'Beta', dot: '#123456' },
]

function setup(props = {}) {
  const onselect = vi.fn()
  const result = render(FilterChips, {
    props: { items, value: '', label: 'Test filter', onselect, ...props },
  })
  return { ...result, onselect }
}

describe('FilterChips', () => {
  test('renders a labelled radiogroup with one radio per item', () => {
    const { container } = setup()
    const group = container.querySelector('[role="radiogroup"]')
    expect(group).toHaveAttribute('aria-label', 'Test filter')
    expect(container.querySelectorAll('[role="radio"]')).toHaveLength(3)
  })

  test('marks the selected chip with aria-checked and .active', () => {
    const { container } = setup({ value: 'a' })
    const chips = container.querySelectorAll('[role="radio"]')
    expect(chips[1]).toHaveAttribute('aria-checked', 'true')
    expect(chips[1]).toHaveClass('active')
    expect(chips[0]).toHaveAttribute('aria-checked', 'false')
  })

  test('roving tabindex: only the selected chip is a tab stop', () => {
    const { container } = setup({ value: 'b' })
    const chips = [...container.querySelectorAll('[role="radio"]')]
    expect(chips.map(c => c.getAttribute('tabindex'))).toEqual(['-1', '-1', '0'])
  })

  test('roving tabindex falls back to the first chip when nothing matches', () => {
    const { container } = setup({ value: 'no-such-value' })
    const chips = [...container.querySelectorAll('[role="radio"]')]
    expect(chips.map(c => c.getAttribute('tabindex'))).toEqual(['0', '-1', '-1'])
  })

  test('clicking a chip selects it', async () => {
    const { container, onselect } = setup()
    await fireEvent.click(container.querySelectorAll('[role="radio"]')[1])
    expect(onselect).toHaveBeenCalledWith('a')
  })

  test('ArrowRight moves selection to the next chip', async () => {
    const { container, onselect } = setup({ value: '' })
    const chips = container.querySelectorAll('[role="radio"]')
    await fireEvent.keyDown(chips[0], { key: 'ArrowRight' })
    expect(onselect).toHaveBeenCalledWith('a')
  })

  test('ArrowDown behaves like ArrowRight', async () => {
    const { container, onselect } = setup({ value: '' })
    const chips = container.querySelectorAll('[role="radio"]')
    await fireEvent.keyDown(chips[0], { key: 'ArrowDown' })
    expect(onselect).toHaveBeenCalledWith('a')
  })

  test('ArrowLeft wraps around to the last chip', async () => {
    const { container, onselect } = setup({ value: '' })
    const chips = container.querySelectorAll('[role="radio"]')
    await fireEvent.keyDown(chips[0], { key: 'ArrowLeft' })
    expect(onselect).toHaveBeenCalledWith('b')
  })

  test('ArrowUp moves to the previous chip', async () => {
    const { container, onselect } = setup({ value: 'b' })
    const chips = container.querySelectorAll('[role="radio"]')
    await fireEvent.keyDown(chips[2], { key: 'ArrowUp' })
    expect(onselect).toHaveBeenCalledWith('a')
  })

  test('ArrowRight wraps from the last chip to the first', async () => {
    const { container, onselect } = setup({ value: 'b' })
    const chips = container.querySelectorAll('[role="radio"]')
    await fireEvent.keyDown(chips[2], { key: 'ArrowRight' })
    expect(onselect).toHaveBeenCalledWith('')
  })

  test('Home and End jump to the first and last chip', async () => {
    const { container, onselect } = setup({ value: 'a' })
    const chips = container.querySelectorAll('[role="radio"]')
    await fireEvent.keyDown(chips[1], { key: 'End' })
    expect(onselect).toHaveBeenCalledWith('b')
    await fireEvent.keyDown(chips[1], { key: 'Home' })
    expect(onselect).toHaveBeenCalledWith('')
  })

  test('arrow navigation moves DOM focus to the target chip', async () => {
    const { container } = setup({ value: '' })
    const chips = container.querySelectorAll('[role="radio"]')
    chips[0].focus()
    await fireEvent.keyDown(chips[0], { key: 'ArrowRight' })
    expect(document.activeElement).toBe(chips[1])
  })

  test('ignores unrelated keys', async () => {
    const { container, onselect } = setup()
    const chips = container.querySelectorAll('[role="radio"]')
    await fireEvent.keyDown(chips[0], { key: 'a' })
    expect(onselect).not.toHaveBeenCalled()
  })

  test('renders an optional colour dot and per-item testid', () => {
    const { container } = setup()
    expect(container.querySelector('[data-testid="chip-alpha"]')).toBeInTheDocument()
    const dots = container.querySelectorAll('.chip-dot')
    expect(dots).toHaveLength(1)
    expect(dots[0].style.background).toBe('rgb(18, 52, 86)')
  })

  test('applies the compact size modifier', () => {
    const { container } = setup({ size: 'sm' })
    expect(container.querySelector('.filter-chips')).toHaveClass('filter-chips-sm')
  })

  test('renders nothing and ignores keys with an empty item list', async () => {
    const { container, onselect } = setup({ items: [] })
    const group = container.querySelector('[role="radiogroup"]')
    expect(container.querySelectorAll('[role="radio"]')).toHaveLength(0)
    await fireEvent.keyDown(group, { key: 'ArrowRight' })
    expect(onselect).not.toHaveBeenCalled()
  })
})

describe('FilterChips (multiple)', () => {
  function setupMulti(props = {}) {
    return setup({ multiple: true, value: [], ...props })
  }
  const chipsOf = (container) => [...container.querySelectorAll('.chip')]

  test('renders a labelled toolbar of toggle buttons instead of radios', () => {
    const { container } = setupMulti()
    const bar = container.querySelector('[role="toolbar"]')
    expect(bar).toHaveAttribute('aria-label', 'Test filter')
    expect(chipsOf(container)).toHaveLength(3)
    expect(container.querySelectorAll('[role="radio"]')).toHaveLength(0)
    expect(container.querySelectorAll('[aria-checked]')).toHaveLength(0)
  })

  test('marks every selected chip', () => {
    const { container } = setupMulti({ value: ['a', 'b'] })
    const chips = chipsOf(container)
    expect(chips.map(c => c.getAttribute('aria-pressed'))).toEqual(['false', 'true', 'true'])
    expect(chips[1]).toHaveClass('active')
    expect(chips[2]).toHaveClass('active')
  })

  test('the All chip is active while nothing is selected', () => {
    const { container } = setupMulti()
    expect(chipsOf(container)[0]).toHaveAttribute('aria-pressed', 'true')
  })

  test('every chip carries a state marker so the bar reads as multi-select', () => {
    const { container } = setupMulti({ value: ['a'] })
    expect(container.querySelectorAll('.chip-mark')).toHaveLength(3)
    // The colour dot is a single-select affordance and must not double up.
    expect(container.querySelectorAll('.chip-dot')).toHaveLength(0)
  })

  test('clicking an unselected chip adds it to the selection', async () => {
    const { container, onselect } = setupMulti({ value: ['a'] })
    await fireEvent.click(chipsOf(container)[2])
    expect(onselect).toHaveBeenCalledWith(['a', 'b'])
  })

  test('clicking a selected chip removes it', async () => {
    const { container, onselect } = setupMulti({ value: ['a', 'b'] })
    await fireEvent.click(chipsOf(container)[1])
    expect(onselect).toHaveBeenCalledWith(['b'])
  })

  test('the All chip clears the whole selection', async () => {
    const { container, onselect } = setupMulti({ value: ['a', 'b'] })
    await fireEvent.click(chipsOf(container)[0])
    expect(onselect).toHaveBeenCalledWith([])
  })

  test('arrows move focus without changing the selection', async () => {
    const { container, onselect } = setupMulti({ value: ['a'] })
    const chips = chipsOf(container)
    chips[1].focus()
    await fireEvent.keyDown(chips[1], { key: 'ArrowRight' })
    expect(document.activeElement).toBe(chips[2])
    expect(onselect).not.toHaveBeenCalled()
  })

  test('roving tabindex anchors on the first selected chip', () => {
    const { container } = setupMulti({ value: ['b'] })
    expect(chipsOf(container).map(c => c.getAttribute('tabindex'))).toEqual(['-1', '-1', '0'])
  })

  test('the tab stop follows the last-focused chip, not the selection', async () => {
    const { container } = setupMulti({ value: ['a'] })
    const chips = chipsOf(container)
    expect(chips[1]).toHaveAttribute('tabindex', '0')
    await fireEvent.keyDown(chips[1], { key: 'End' })
    expect(chips[2]).toHaveAttribute('tabindex', '0')
    expect(chips[1]).toHaveAttribute('tabindex', '-1')
  })

  test('tolerates a scalar value from a single-select caller', () => {
    const { container } = setupMulti({ value: 'a' })
    expect(chipsOf(container)[1]).toHaveAttribute('aria-pressed', 'true')
  })
})
