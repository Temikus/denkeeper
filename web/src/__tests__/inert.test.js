import { describe, test, expect } from 'vitest'
import { inert } from '../inert.js'

describe('inert action', () => {
  test('sets the attribute when initialised truthy', () => {
    const el = document.createElement('div')
    inert(el, true)
    expect(el.hasAttribute('inert')).toBe(true)
  })

  test('leaves the attribute off when initialised falsy', () => {
    const el = document.createElement('div')
    inert(el, false)
    expect(el.hasAttribute('inert')).toBe(false)
  })

  test('update toggles the attribute both ways', () => {
    const el = document.createElement('div')
    const handle = inert(el, true)
    handle.update(false)
    expect(el.hasAttribute('inert')).toBe(false)
    handle.update(true)
    expect(el.hasAttribute('inert')).toBe(true)
  })

  test('returns focus to the opener when the panel closes', () => {
    const trigger = document.createElement('button')
    const panel = document.createElement('div')
    const field = document.createElement('input')
    panel.appendChild(field)
    document.body.append(trigger, panel)

    const handle = inert(panel, true)
    trigger.focus()
    handle.update(false) // open
    field.focus()
    expect(document.activeElement).toBe(field)

    handle.update(true) // close
    expect(document.activeElement).toBe(trigger)

    trigger.remove()
    panel.remove()
  })

  test('leaves focus alone when it is outside the closing panel', () => {
    const outside = document.createElement('button')
    const trigger = document.createElement('button')
    const panel = document.createElement('div')
    document.body.append(outside, trigger, panel)

    const handle = inert(panel, true)
    trigger.focus()
    handle.update(false)
    outside.focus()
    handle.update(true)
    expect(document.activeElement).toBe(outside)

    outside.remove()
    trigger.remove()
    panel.remove()
  })

  test('blurs instead of restoring when the opener is gone', () => {
    const trigger = document.createElement('button')
    const panel = document.createElement('div')
    const field = document.createElement('input')
    panel.appendChild(field)
    document.body.append(trigger, panel)

    const handle = inert(panel, true)
    trigger.focus()
    handle.update(false)
    field.focus()
    trigger.remove()

    handle.update(true)
    expect(document.activeElement).not.toBe(field)

    panel.remove()
  })

  test('ignores repeated updates with the same value', () => {
    const el = document.createElement('div')
    const handle = inert(el, true)
    handle.update(true)
    expect(el.hasAttribute('inert')).toBe(true)
  })
})
