import { describe, test, expect } from 'vitest'
import { computeBreakpoints, popularityBars } from '../popularity.js'

describe('computeBreakpoints', () => {
  test('returns null for an empty list', () => {
    expect(computeBreakpoints([])).toBeNull()
  })

  test('returns null when no model has weekly_tokens', () => {
    expect(computeBreakpoints([{ id: 'a' }, { id: 'b', weekly_tokens: 0 }])).toBeNull()
  })

  test('ignores zero and missing weekly_tokens when picking thresholds', () => {
    const models = [
      { weekly_tokens: 100 },
      { weekly_tokens: 0 },
      { id: 'no-tokens' },
      { weekly_tokens: 200 },
    ]
    const bps = computeBreakpoints(models)
    // Only 100 and 200 contribute; thresholds collapse to those two values.
    expect(bps).toHaveLength(4)
    for (const bp of bps) {
      expect([100, 200]).toContain(bp.threshold)
    }
  })

  test('thresholds are derived from the input distribution', () => {
    // 100 evenly log-spaced values: weekly_tokens = 10^(2 + 9*i/99) for i=0..99
    const models = []
    for (let i = 0; i < 100; i++) {
      models.push({ weekly_tokens: Math.pow(10, 2 + (9 * i) / 99) })
    }
    const bps = computeBreakpoints(models)
    expect(bps.map(b => b.bars)).toEqual([5, 4, 3, 2])
    // Cuts are floor(p * length): 97, 90, 70, 40 → values at those indices.
    expect(bps[0].threshold).toBeCloseTo(Math.pow(10, 2 + (9 * 97) / 99), -3)
    expect(bps[1].threshold).toBeCloseTo(Math.pow(10, 2 + (9 * 90) / 99), -3)
    expect(bps[2].threshold).toBeCloseTo(Math.pow(10, 2 + (9 * 70) / 99), -3)
    expect(bps[3].threshold).toBeCloseTo(Math.pow(10, 2 + (9 * 40) / 99), -3)
  })

  test('shifts when the distribution shifts (data-adaptive)', () => {
    const lowVolume = Array.from({ length: 50 }, (_, i) => ({ weekly_tokens: 1000 + i }))
    const highVolume = Array.from({ length: 50 }, (_, i) => ({ weekly_tokens: 1e10 + i }))
    const lowBps = computeBreakpoints(lowVolume)
    const highBps = computeBreakpoints(highVolume)
    expect(lowBps[0].threshold).toBeLessThan(highBps[0].threshold)
  })
})

describe('popularityBars', () => {
  // 100 evenly log-spaced values from 100 to 1e11
  const models = []
  for (let i = 0; i < 100; i++) {
    models.push({ weekly_tokens: Math.pow(10, 2 + (9 * i) / 99) })
  }
  const bps = computeBreakpoints(models)

  test('returns 0 for missing or zero tokens', () => {
    expect(popularityBars(0, bps)).toBe(0)
    expect(popularityBars(null, bps)).toBe(0)
    expect(popularityBars(undefined, bps)).toBe(0)
  })

  test('returns 0 when breakpoints are unavailable', () => {
    expect(popularityBars(1000, null)).toBe(0)
  })

  test('top of distribution gets 5 bars', () => {
    expect(popularityBars(bps[0].threshold, bps)).toBe(5)
    expect(popularityBars(bps[0].threshold * 10, bps)).toBe(5)
  })

  test('bucketing across all five tiers', () => {
    expect(popularityBars(bps[0].threshold, bps)).toBe(5) // p97
    expect(popularityBars(bps[1].threshold, bps)).toBe(4) // p90
    expect(popularityBars(bps[2].threshold, bps)).toBe(3) // p70
    expect(popularityBars(bps[3].threshold, bps)).toBe(2) // p40
    expect(popularityBars(1, bps)).toBe(1)               // below p40
  })

  test('boundary values fall into the higher tier', () => {
    // Anything >= a threshold gets that threshold's bar count.
    const justBelowP97 = bps[0].threshold - 1
    expect(popularityBars(justBelowP97, bps)).toBe(4)
    expect(popularityBars(bps[0].threshold, bps)).toBe(5)
  })

  test('handles a single-model distribution', () => {
    const single = computeBreakpoints([{ weekly_tokens: 5000 }])
    expect(popularityBars(5000, single)).toBe(5)
    expect(popularityBars(1, single)).toBe(1)
  })
})
