import { describe, test, expect } from 'vitest'
import { inapplicableKinds } from '../evalCategories.js'

describe('inapplicableKinds', () => {
  test('names each kind the agent detail rules out', () => {
    expect(inapplicableKinds({ command_skills: [], schedules: [], has_tools: false })).toEqual({
      skill_command: 'no command skills',
      scheduled: 'no schedules',
      tool_heavy: 'no tools',
    })
  })

  test('an agent with each capability rules nothing out', () => {
    expect(inapplicableKinds({ command_skills: ['report'], schedules: ['morning'], has_tools: true })).toEqual({})
  })

  test('missing fields or no detail say nothing', () => {
    expect(inapplicableKinds({ name: 'default' })).toEqual({})
    expect(inapplicableKinds(null)).toEqual({})
  })
})
