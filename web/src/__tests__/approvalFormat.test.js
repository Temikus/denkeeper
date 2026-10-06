import { describe, test, expect } from 'vitest'
import { parseToolCall } from '../approvalFormat.js'

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
