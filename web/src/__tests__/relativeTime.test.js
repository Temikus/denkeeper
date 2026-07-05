import { describe, test, expect } from 'vitest'
import { relativeTime, shortAbsolute } from '../relativeTime.js'

// Fixed reference point, built in local time so day boundaries are stable
// regardless of the runner's timezone.
const NOW = new Date(2026, 0, 15, 12, 0, 0).getTime()
const at = (...args) => new Date(...args).toISOString()

describe('relativeTime', () => {
  test('returns empty string for falsy or invalid input', () => {
    expect(relativeTime(null, NOW)).toBe('')
    expect(relativeTime('', NOW)).toBe('')
    expect(relativeTime('not-a-date', NOW)).toBe('')
  })

  test('past tiers: seconds, minutes, hours, days', () => {
    expect(relativeTime(NOW - 30 * 1000, NOW)).toBe('30s ago')
    expect(relativeTime(NOW - 45 * 60 * 1000, NOW)).toBe('45m ago')
    expect(relativeTime(NOW - 2 * 3600 * 1000, NOW)).toBe('2h ago')
    expect(relativeTime(NOW - 3 * 86400 * 1000, NOW)).toBe('3d ago')
  })

  test('future tiers include a second unit for hours and days', () => {
    expect(relativeTime(NOW + 45 * 60 * 1000, NOW)).toBe('in 45m')
    expect(relativeTime(NOW + (2 * 3600 + 15 * 60) * 1000, NOW)).toBe('in 2h 15m')
    expect(relativeTime(NOW + 2 * 3600 * 1000, NOW)).toBe('in 2h')
    expect(relativeTime(NOW + (3 * 86400 + 4 * 3600) * 1000, NOW)).toBe('in 3d 4h')
    expect(relativeTime(NOW + 3 * 86400 * 1000, NOW)).toBe('in 3d')
  })
})

describe('shortAbsolute', () => {
  test('returns empty string for falsy or invalid input', () => {
    expect(shortAbsolute(null, NOW)).toBe('')
    expect(shortAbsolute('not-a-date', NOW)).toBe('')
  })

  test('same day renders as today', () => {
    expect(shortAbsolute(at(2026, 0, 15, 14, 30), NOW)).toBe('today 14:30')
  })

  test('next day renders as tomorrow', () => {
    expect(shortAbsolute(at(2026, 0, 16, 8, 0), NOW)).toBe('tomorrow 08:00')
  })

  test('previous day renders as yesterday', () => {
    expect(shortAbsolute(at(2026, 0, 14, 18, 5), NOW)).toBe('yesterday 18:05')
  })

  test('other days render as short date plus time', () => {
    expect(shortAbsolute(at(2026, 2, 5, 9, 0), NOW)).toBe('Mar 5 09:00')
  })
})
