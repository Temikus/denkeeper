import { describe, test, expect } from 'vitest'
import { parseToolCall, shadowLine } from '../approvalFormat.js'

const shadowOf = (verdict, extra = {}) => ({ agent: 'pamela', decider: 'jev', verdict, lowest: 'scoped', score: 0.978, ...extra })
const card = (status, shadow, extra = {}) => ({ status, shadow, ...extra })
const text = line => line && line.verdict + line.detail

describe('shadowLine', () => {
  test('a pending card leads with what the decider would do, then the lowest check, floored', () => {
    const approve = shadowLine(card('pending', shadowOf('APPROVE')))
    expect(approve).toMatchObject({ kind: 'evidence', verdict: 'jev would approve', tone: 'approve' })
    expect(text(approve)).toBe('jev would approve · lowest: scoped 0.97 · shadow mode, so you still decide')
    const deny = shadowLine(card('pending', shadowOf('DENY', { lowest: 'no_self_grant', score: 0.041 })))
    expect(deny.tone).toBe('deny')
    expect(text(deny)).toBe('jev would deny · lowest: no self-grant 0.04 · shadow mode, so you still decide')
  })

  test('a pending card the decider would escalate reads as unsure, in no colour', () => {
    const line = shadowLine(card('pending', shadowOf('ESCALATE')))
    expect(line.tone).toBeNull()
    expect(text(line)).toBe('jev is unsure · lowest: scoped 0.97 · shadow mode, so you still decide')
  })

  test('a pending card with no score still says what the decider would do', () => {
    const line = shadowLine(card('pending', shadowOf('ESCALATE', { lowest: '', score: undefined })))
    expect(text(line)).toBe('jev is unsure · shadow mode, so you still decide')
  })

  test('would have approved, when the call was denied or escalated, with the lowest check', () => {
    for (const status of ['supervisor_denied', 'supervisor_escalated', 'denied']) {
      const line = shadowLine(card(status, shadowOf('APPROVE')))
      expect(line).toMatchObject({ kind: 'disagree', verdict: 'jev would have approved', tone: 'approve' })
      expect(text(line)).toBe('jev would have approved · scoped 0.97 · shadow mode, so this had no effect')
    }
  })

  test('would have denied, when the call was approved', () => {
    for (const status of ['supervisor_approved', 'approved']) {
      const line = shadowLine(card(status, shadowOf('DENY', { lowest: 'aligned', score: 0.04 })))
      expect(line).toMatchObject({ kind: 'disagree', verdict: 'jev would have denied', tone: 'deny' })
      expect(text(line)).toBe('jev would have denied · aligned 0.04 · shadow mode, so this had no effect')
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

  test('a human answer to an escalated call has no line: the escalated card shows it', () => {
    const escalated = card('supervisor_escalated', shadowOf('APPROVE'), { tool_id: 'tc-1' })
    const answered = card('denied', shadowOf('APPROVE'), { tool_id: 'tc-1' })
    expect(shadowLine(answered, [escalated, answered])).toBeNull()
    expect(shadowLine(escalated, [escalated, answered])).not.toBeNull()
    // Another call's escalation does not hide this one.
    expect(shadowLine(answered, [{ ...escalated, tool_id: 'tc-2' }, answered])).not.toBeNull()
  })

  test('the link names the reviewed agent, URL-encoded', () => {
    const line = shadowLine(card('supervisor_denied', shadowOf('APPROVE', { agent: 'ops/bot #1' })))
    expect(line.href).toBe('#/agents/ops%2Fbot%20%231?card=permission')
  })

  test('a disagreement with no agent keeps its text but has no link', () => {
    const line = shadowLine(card('supervisor_denied', shadowOf('APPROVE', { agent: '' })))
    expect(line.verdict).toBe('jev would have approved')
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
