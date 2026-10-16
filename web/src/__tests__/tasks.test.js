import { describe, test, expect } from 'vitest'
import { columnOf, memberName, isWaitingOnYou, cardMeta, eventLine, assignHint, heldFor } from '../tasks.js'

const members = [{ id: 'agent:pamela', name: 'pamela', kind: 'agent' }]

describe('tasks display rules', () => {
  test('blocked stays with In progress and cancelled folds into Done', () => {
    expect(columnOf('blocked')).toBe('in_progress')
    expect(columnOf('cancelled')).toBe('done')
    expect(columnOf('todo')).toBe('todo')
  })

  test('member names', () => {
    expect(memberName('', members)).toBe('Unassigned')
    expect(memberName('human', members)).toBe('You')
    expect(memberName('agent:pamela', members)).toBe('pamela')
    expect(memberName('ext:claude-code', members)).toBe('claude-code')
  })

  test('waiting on you: blocked, your review, assigned to you; never closed', () => {
    expect(isWaitingOnYou({ status: 'blocked', assignee: 'agent:pamela' })).toBe(true)
    expect(isWaitingOnYou({ status: 'review', reviewer: 'human', assignee: 'agent:pamela' })).toBe(true)
    expect(isWaitingOnYou({ status: 'review', reviewer: 'agent:pamela', assignee: 'human' })).toBe(false)
    expect(isWaitingOnYou({ status: 'todo', assignee: 'human' })).toBe(true)
    expect(isWaitingOnYou({ status: 'done', assignee: 'human' })).toBe(false)
    expect(isWaitingOnYou({ status: 'todo', assignee: '' })).toBe(false)
  })

  test('card meta says who can act next', () => {
    const now = Date.parse('2026-10-16T09:16:00Z')
    expect(cardMeta({ status: 'in_progress', held_at: '2026-10-16T09:02:00Z' }, now)).toBe('held 14m')
    expect(cardMeta({ status: 'todo', assignee: '' })).toBe('anyone may claim')
    expect(cardMeta({ status: 'todo', assignee: 'ext:claude-code' })).toBe('waits for check-in')
    expect(cardMeta({ status: 'todo', assignee: 'human', created_by: 'agent:pamela' })).toBe('filed by pamela')
    expect(cardMeta({ status: 'review', reviewer: 'human' })).toBe('your review')
  })

  test('a hold time ahead of the browser clock reads as just held', () => {
    const now = Date.parse('2026-10-16T09:00:00Z')
    expect(heldFor('2026-10-16T09:05:00Z', now)).toBe('0s')
    expect(heldFor('2026-10-16T08:00:00Z', now)).toBe('1h')
  })

  test('event lines', () => {
    expect(eventLine({ actor: 'human', kind: 'created', data: '{"assignee":"agent:pamela"}' }, members)).toBe('You filed it for pamela')
    expect(eventLine({ actor: 'agent:pamela', kind: 'status', body: 'No key', data: '{"from":"in_progress","to":"blocked"}' }, members)).toBe('pamela marked it blocked: No key')
    expect(eventLine({ actor: 'human', kind: 'released', data: '{"holder":"agent:pamela"}' }, members)).toBe("You released pamela's claim")
    expect(eventLine({ actor: 'human', kind: 'assigned', data: '{"from":"agent:pamela","to":""}' }, members)).toBe('You unassigned it')
    expect(eventLine({ actor: 'agent:pamela', kind: 'subtask', body: 'Rate limits', data: '{"child_id":134}' }, members)).toBe('pamela filed DK-134: Rate limits')
    expect(eventLine({ actor: 'human', kind: 'status', data: 'not json' }, members)).toBe('You changed its status')
  })

  test('assign hint never promises an agent starts now', () => {
    expect(assignHint('', members)).toMatch(/Anyone may claim/)
    expect(assignHint('agent:pamela', members)).toBe('pamela picks it up when it next checks its tasks.')
  })
})
