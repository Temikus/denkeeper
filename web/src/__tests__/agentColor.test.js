import { describe, test, expect } from 'vitest'
import { agentColor } from '../agentColor.js'

describe('agentColor', () => {
  test('is deterministic for the same name', () => {
    expect(agentColor('default')).toBe(agentColor('default'))
    expect(agentColor('argus')).toBe(agentColor('argus'))
  })

  test('returns an hsl color string', () => {
    expect(agentColor('default')).toMatch(/^hsl\(\d+ 55% 45%\)$/)
  })

  test('distinct names produce distinct hues', () => {
    const colors = new Set(['default', 'argus', 'helper', 'reviewer'].map(agentColor))
    expect(colors.size).toBe(4)
  })

  test('handles empty and missing input without throwing', () => {
    expect(agentColor('')).toMatch(/^hsl\(/)
    expect(agentColor(undefined)).toMatch(/^hsl\(/)
  })
})
