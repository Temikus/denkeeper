import { describe, test, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// --accent and --danger answer different questions ("this is on" vs "this
// destroys something"), so they must stay far enough apart to be told apart
// across a screen, not just side by side. Light mode regressed to ΔE 11 once
// (issue #304); this is the guard against drifting back together.
const MIN_DELTA_E = 20

// .btn-primary and .chip.active put white text on the accent fill.
const MIN_CONTRAST = 4.5

// jsdom rewrites import.meta.url to an http URL, so resolve from the vitest
// root (web/) instead.
const appSvelte = readFileSync(resolve('src/App.svelte'), 'utf8')

function themeBlock(selector) {
  const start = appSvelte.indexOf(`:global(${selector}) {`)
  if (start === -1) throw new Error(`no :global(${selector}) block in App.svelte`)
  return appSvelte.slice(start, appSvelte.indexOf('\n  }', start))
}

function token(block, name) {
  const match = block.match(new RegExp(`--${name}:\\s*([^;]+);`))
  if (!match) throw new Error(`token --${name} not found`)
  return match[1].trim()
}

function rgb(hex) {
  const n = parseInt(hex.replace('#', ''), 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}

function linearize(c) {
  const s = c / 255
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}

function relativeLuminance(hex) {
  const [r, g, b] = rgb(hex).map(linearize)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrastOnWhite(hex) {
  return 1.05 / (relativeLuminance(hex) + 0.05)
}

// CIE-Lab under D65.
function lab(hex) {
  const [r, g, b] = rgb(hex).map(linearize)
  const x = (0.4124 * r + 0.3576 * g + 0.1805 * b) / 0.95047
  const y = 0.2126 * r + 0.7152 * g + 0.0722 * b
  const z = (0.0193 * r + 0.1192 * g + 0.9505 * b) / 1.08883
  const f = (t) => (t > 216 / 24389 ? Math.cbrt(t) : (841 / 108) * t + 4 / 29)
  const [fx, fy, fz] = [f(x), f(y), f(z)]
  return [116 * fy - 16, 500 * (fx - fy), 200 * (fy - fz)]
}

function deltaE(a, b) {
  const [l1, a1, b1] = lab(a)
  const [l2, a2, b2] = lab(b)
  return Math.hypot(l1 - l2, a1 - a2, b1 - b2)
}

describe.each([
  ['light', ':root'],
  ['dark', ':root.dark'],
])('%s theme palette', (_name, selector) => {
  const block = themeBlock(selector)
  const accent = token(block, 'accent')
  const danger = token(block, 'danger')

  test('--accent is distinguishable from --danger', () => {
    expect(deltaE(accent, danger)).toBeGreaterThanOrEqual(MIN_DELTA_E)
  })

  test('--accent-hover is distinguishable from --danger', () => {
    expect(deltaE(token(block, 'accent-hover'), danger)).toBeGreaterThanOrEqual(MIN_DELTA_E)
  })

  test('--accent-rgb is the same colour as --accent', () => {
    const parsed = token(block, 'accent-rgb').split(',').map((n) => Number(n.trim()))
    expect(parsed).toEqual(rgb(accent))
  })
})

describe('light theme contrast', () => {
  const block = themeBlock(':root')

  // Only light mode uses white text on the accent fill; dark mode's accent is
  // a light tint that carries dark text.
  test('white text on --accent clears AA', () => {
    expect(contrastOnWhite(token(block, 'accent'))).toBeGreaterThanOrEqual(MIN_CONTRAST)
  })

  test('white text on --accent-hover clears AA', () => {
    expect(contrastOnWhite(token(block, 'accent-hover'))).toBeGreaterThanOrEqual(MIN_CONTRAST)
  })
})
