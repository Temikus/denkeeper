import { describe, test, expect } from 'vitest'
import { parseToolCall, shadowLine } from '../approvalFormat.js'

const shadowOf = (verdict, extra = {}) => ({ agent: 'pamela', decider: 'jev', verdict, lowest: 'scoped', score: 0.978, ...extra })
const card = (status, shadow) => ({ status, shadow })

describe('shadowLine', () => {
  test('a pending card with a score shows the lowest check as evidence, floored', () => {
    expect(shadowLine(card('pending', shadowOf('ESCALATE')))).toEqual({ kind: 'evidence', text: 'jev lowest check: scoped 0.97' })
    expect(shadowLine(card('pending', shadowOf('APPROVE', { lowest: 'no_self_grant', score: 0.991 }))))
      .toEqual({ kind: 'evidence', text: 'jev lowest check: no self-grant 0.99' })
  })

  test('a pending card with no score shows nothing', () => {
    expect(shadowLine(card('pending', shadowOf('ESCALATE', { lowest: '', score: undefined })))).toBeNull()
  })

  test('would have approved, when the call was denied or escalated', () => {
    for (const status of ['supervisor_denied', 'supervisor_escalated', 'denied']) {
      expect(shadowLine(card(status, shadowOf('APPROVE')))).toMatchObject({ kind: 'disagree', text: 'jev would have approved' })
    }
  })

  test('would have denied, when the call was approved', () => {
    for (const status of ['supervisor_approved', 'approved']) {
      expect(shadowLine(card(status, shadowOf('DENY')))).toMatchObject({ kind: 'disagree', text: 'jev would have denied' })
    }
  })

  test('nothing when the verdicts agree, the decider would escalate, or there is no shadow', () => {
    expect(shadowLine(card('supervisor_approved', shadowOf('APPROVE')))).toBeNull()
    expect(shadowLine(card('supervisor_denied', shadowOf('DENY')))).toBeNull()
    expect(shadowLine(card('supervisor_denied', shadowOf('ESCALATE')))).toBeNull()
    expect(shadowLine(card('approved', shadowOf('ESCALATE')))).toBeNull()
    expect(shadowLine(card('supervisor_denied', null))).toBeNull()
    expect(shadowLine(card('pending', undefined))).toBeNull()
  })

  test('the link names the reviewed agent, URL-encoded', () => {
    const line = shadowLine(card('supervisor_denied', shadowOf('APPROVE', { agent: 'ops/bot #1' })))
    expect(line.href).toBe('#/agents/ops%2Fbot%20%231?card=permission')
  })

  test('a disagreement with no agent keeps its text but has no link', () => {
    const line = shadowLine(card('supervisor_denied', shadowOf('APPROVE', { agent: '' })))
    expect(line.text).toBe('jev would have approved')
    expect(line.href).toBeNull()
  })
})

const args = JSON.stringify({
  code: 'var a = 1;\nreturn a;',
  input: { emails: [{ id: 'x' }] },
  limit: 5,
  query: 'short',
})

describe('parseToolCall', () => {
  test('splits an engine summary into tool name and args', () => {
    const call = parseToolCall({
      summary: `Execute tool "run_javascript" with args: ${args}`,
      payload: args,
    })
    expect(call.tool).toBe('run_javascript')
    expect(call.retry).toBe('')
    expect(call.args).toEqual([
      { key: 'code', value: 'var a = 1;\nreturn a;', block: true },
      { key: 'input', value: '{\n  "emails": [\n    {\n      "id": "x"\n    }\n  ]\n}', block: true },
      { key: 'limit', value: '5', block: false },
      { key: 'query', value: 'short', block: false },
    ])
  })

  test('reads the retry prefix', () => {
    const call = parseToolCall({ summary: '[retry 1/2] Execute tool "web_fetch" with args: {}', payload: '{}' })
    expect(call.tool).toBe('web_fetch')
    expect(call.retry).toBe('retry 1/2')
    expect(call.args).toEqual([])
  })

  test('falls back to the summary tail when payload is missing', () => {
    const call = parseToolCall({ summary: 'Execute tool "kv_get" with args: {"key":"a"}' })
    expect(call.args).toEqual([{ key: 'key', value: 'a', block: false }])
  })

  test('keeps non-object args raw', () => {
    const call = parseToolCall({ summary: 'Execute tool "t" with args: not json', payload: 'not json' })
    expect(call.args).toBeNull()
    expect(call.raw).toBe('not json')
  })

  test('returns null for other approval kinds', () => {
    expect(parseToolCall({ summary: 'Update USER.md', payload: '# me' })).toBeNull()
  })
})
