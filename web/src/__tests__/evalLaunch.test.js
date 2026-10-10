import { describe, test, expect } from 'vitest'
import { pickBaseAgent, turnsLine } from '../evalLaunch.js'

describe('pickBaseAgent', () => {
  test('when every agent supervises another, it falls back to the first', () => {
    const agents = [{ name: 'a', supervisor: 'b' }, { name: 'b', supervisor: 'a' }]
    expect(pickBaseAgent(agents, '', '')).toBe('a')
  })

  test('no agents means no agent', () => {
    expect(pickBaseAgent([], 'x', 'y')).toBe('')
  })
})

describe('turnsLine', () => {
  test('singular case and run', () => {
    expect(turnsLine({ tasks: 1, k: 1 })).toBe('1 case × 1 run × 2 models = 2 turns')
  })

  test('nothing to count says nothing', () => {
    expect(turnsLine({ tasks: 0, k: 3 })).toBe('')
  })
})
