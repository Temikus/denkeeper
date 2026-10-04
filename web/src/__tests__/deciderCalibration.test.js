import { describe, test, expect } from 'vitest'
import { verdict, summarize, histogram, disagreements, suggestion } from '../deciderCalibration.js'

const row = (min, supervisor, extra = {}) => ({
  min_score: min, supervisor, decider_cost: 0.0001, supervisor_cost: 0.04, time: '2026-10-01T00:00:00Z', ...extra,
})

describe('verdict', () => {
  test('thresholds are inclusive and deny is checked first', () => {
    expect(verdict(row(0.95), 0.95, 0.05)).toBe('APPROVE')
    expect(verdict(row(0.9499), 0.95, 0.05)).toBe('ESCALATE')
    expect(verdict(row(0.05), 0.95, 0.05)).toBe('DENY')
    // Overlapping thresholds cannot be saved, but deny still wins here.
    expect(verdict(row(0.5), 0.4, 0.6)).toBe('DENY')
  })

  test('a review with a missing answer always escalates', () => {
    expect(verdict(row(null), 0.01, 0.001)).toBe('ESCALATE')
  })
})

describe('summarize', () => {
  test('counts settled calls and both kinds of disagreement', () => {
    const rows = [
      row(0.99, 'APPROVE'),
      row(0.97, 'DENY'),      // unsafe approval
      row(0.96, 'ESCALATE'),  // unsafe approval
      row(0.5, 'APPROVE'),    // escalated
      row(0.02, 'APPROVE'),   // wrong denial
      row(0.01, 'DENY'),
    ]
    const s = summarize(rows, 0.95, 0.05)
    expect(s).toMatchObject({ total: 6, settled: 5, escalated: 1, unsafeApprovals: 2, wrongDenials: 1, costKnown: 5 })
    expect(s.avoidedUSD).toBeCloseTo(0.2)
    expect(s.deciderUSD).toBeCloseTo(0.0006)
  })

  test('spend avoided counts only reviews with a recorded supervisor cost', () => {
    const s = summarize([row(0.99, 'APPROVE', { supervisor_cost: null }), row(0.99, 'APPROVE')], 0.95, 0.05)
    expect(s.settled).toBe(2)
    expect(s.costKnown).toBe(1)
    expect(s.avoidedUSD).toBeCloseTo(0.04)
  })

  test('a decider approval without a supervisor verdict is not a disagreement', () => {
    expect(summarize([row(0.99, '')], 0.95, 0.05).unsafeApprovals).toBe(0)
  })
})

describe('histogram', () => {
  test('bins by score and splits by supervisor verdict', () => {
    const bins = histogram([row(0, 'DENY'), row(0.04, 'APPROVE'), row(1, 'APPROVE'), row(0.97, ''), row(null, 'APPROVE')])
    expect(bins).toHaveLength(20)
    expect(bins[0]).toMatchObject({ DENY: 1, APPROVE: 1, total: 2 })
    expect(bins[19]).toMatchObject({ APPROVE: 1, none: 1, total: 2 })
    expect(bins.reduce((n, b) => n + b.total, 0)).toBe(4)
  })
})

describe('disagreements', () => {
  test('lists approvals the supervisor denied first, then escalated, then wrong denials', () => {
    const rows = [
      row(0.02, 'APPROVE', { tool: 'deny' }),
      row(0.96, 'ESCALATE', { tool: 'escalated' }),
      row(0.97, 'DENY', { tool: 'old-denied', time: '2026-09-01T00:00:00Z' }),
      row(0.98, 'DENY', { tool: 'new-denied', time: '2026-10-02T00:00:00Z' }),
      row(0.99, 'APPROVE', { tool: 'agreed' }),
    ]
    expect(disagreements(rows, 0.95, 0.05).map(d => d.tool)).toEqual(['new-denied', 'old-denied', 'escalated', 'deny'])
  })
})

describe('suggestion', () => {
  test('raises approve just above the highest unsafe approval', () => {
    const rows = [row(0.93, 'DENY'), row(0.91, 'ESCALATE'), row(0.99, 'APPROVE'), row(0.94, 'APPROVE')]
    const s = suggestion(rows, 0.9, 0.05)
    expect(s).toMatchObject({ kind: 'approve', value: 0.94, count: 2, removed: 2, settledAfter: 2 })
  })

  test('gives no value when only a threshold of 1 would do', () => {
    expect(suggestion([row(0.995, 'DENY')], 0.95, 0.05)).toMatchObject({ kind: 'approve', value: null, count: 1 })
  })

  test('falls back to 0.99 when that removes some of the unsafe approvals', () => {
    const s = suggestion([row(0.995, 'DENY'), row(0.96, 'DENY'), row(0.97, 'ESCALATE')], 0.95, 0.05)
    expect(s).toMatchObject({ kind: 'approve', value: 0.99, count: 3, removed: 2, settledAfter: 1 })
  })

  test('falls back to 0.01 for deny when only 0 would remove every wrong denial', () => {
    const s = suggestion([row(0.005, 'APPROVE'), row(0.04, 'APPROVE')], 0.95, 0.05)
    expect(s).toMatchObject({ kind: 'deny', value: 0.01, count: 2, removed: 1, settledAfter: 1 })
  })

  test('lowers deny just below the lowest wrong denial', () => {
    const s = suggestion([row(0.04, 'APPROVE'), row(0.01, 'DENY')], 0.95, 0.05)
    expect(s).toMatchObject({ kind: 'deny', value: 0.03, count: 1, removed: 1, settledAfter: 1 })
  })

  test('is null when the decider agrees with the supervisor', () => {
    expect(suggestion([row(0.99, 'APPROVE'), row(0.01, 'DENY')], 0.95, 0.05)).toBeNull()
  })
})
