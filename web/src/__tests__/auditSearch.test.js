import { describe, test, expect } from 'vitest'
import { parseAuditSearch } from '../auditSearch.js'

describe('parseAuditSearch', () => {
  test('plain text passes through as the search term', () => {
    expect(parseAuditSearch('database error')).toEqual({
      agent: '', search: 'database error',
    })
  })

  test('empty input yields no filters', () => {
    expect(parseAuditSearch('')).toEqual({ agent: '', search: '' })
    expect(parseAuditSearch(undefined)).toEqual({ agent: '', search: '' })
  })

  test('agent: maps to the exact-match agent filter and leaves the search empty', () => {
    expect(parseAuditSearch('agent:planner')).toEqual({ agent: 'planner', search: '' })
  })

  test('tool: folds into the search term', () => {
    expect(parseAuditSearch('tool:web_search')).toEqual({ agent: '', search: 'web_search' })
  })

  test('tokens combine with each other and with free text', () => {
    expect(parseAuditSearch('agent:planner tool:web_search timeout')).toEqual({
      agent: 'planner', search: 'web_search timeout',
    })
  })

  // The server takes one ?search=, applied as a single summary LIKE, so
  // repeated tool: tokens are a phrase match — not two independent filters.
  test('repeated tool: tokens join into one phrase', () => {
    expect(parseAuditSearch('tool:a tool:b').search).toBe('a b')
  })

  test('prefixes are case-insensitive', () => {
    expect(parseAuditSearch('Agent:planner').agent).toBe('planner')
    expect(parseAuditSearch('TOOL:kv_get').search).toBe('kv_get')
  })

  test('the last agent: wins, since the API takes one agent', () => {
    expect(parseAuditSearch('agent:planner agent:default').agent).toBe('default')
  })

  test('quoted values survive whitespace', () => {
    expect(parseAuditSearch('agent:"my agent"').agent).toBe('my agent')
    expect(parseAuditSearch('"exact phrase"').search).toBe('exact phrase')
  })

  test('unknown prefixes stay literal search text', () => {
    expect(parseAuditSearch('chan:general').search).toBe('chan:general')
    expect(parseAuditSearch('https://example.com').search).toBe('https://example.com')
  })

  test('a bare prefix mid-typing is neither a filter nor literal text', () => {
    expect(parseAuditSearch('agent:')).toEqual({ agent: '', search: '' })
  })
})
