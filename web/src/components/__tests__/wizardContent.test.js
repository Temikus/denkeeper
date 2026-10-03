import { describe, test, expect } from 'vitest'
import { pickModel, toneForTheme, sampleGreeting, houseRules, providerMeta, DEFAULT_HOUSE_RULES, TONES } from '../wizard/wizardContent.js'

describe('wizardContent', () => {
  test('pickModel prefers the role family and the newest-looking ID', () => {
    const models = ['claude-haiku-4-5', 'claude-sonnet-4-5', 'claude-sonnet-5-5']
    expect(pickModel('anthropic', models, 'main')).toBe('claude-sonnet-5-5')
    expect(pickModel('anthropic', models, 'supervisor')).toBe('claude-haiku-4-5')
  })

  test('pickModel falls back to the first model, or empty', () => {
    expect(pickModel('ollama', ['llama3.2', 'qwen'])).toBe('llama3.2')
    expect(pickModel('anthropic', [])).toBe('')
  })

  test('toneForTheme maps presets and treats anything else as custom', () => {
    expect(toneForTheme(TONES[1].theme)).toBe('concise')
    expect(toneForTheme('pirate captain')).toBe('custom')
    expect(toneForTheme('')).toBe('generalist')
  })

  test('sampleGreeting uses the name', () => {
    expect(sampleGreeting('concise', 'Den')).toBe('Den here. What do you need?')
    expect(sampleGreeting('generalist', '')).toContain('your assistant')
  })

  test('houseRules splits paragraphs', () => {
    expect(houseRules(DEFAULT_HOUSE_RULES)).toHaveLength(5)
    expect(houseRules('a\n\n\n b ')).toEqual(['a', 'b'])
  })

  test('providerMeta knows which types need a key', () => {
    expect(providerMeta('ollama').needsKey).toBe(false)
    expect(providerMeta('openrouter').needsKey).toBe(true)
  })
})
