// Parses the audit search box into the API filters it actually maps to.
//
//   agent:<name>  -> the server's exact-match ?agent= filter
//   tool:<name>   -> folded into the free-text ?search= term
//
// `tool:` is a search term rather than its own filter because audit_events has
// no tool column: a tool call's summary IS the tool name, and approval,
// supervisor and error summaries embed it, so a summary match is the widest
// correct reading. Unprefixed words keep their old meaning and pass through
// verbatim, joined by spaces — the server matches the joined string as one
// LIKE phrase, so multi-word input stays a phrase search as it always was.
//
// Only `agent:` and `tool:` are prefixes; anything else with a colon in it
// (`http://x`, `chan:general`) stays literal search text.

const PREFIXED = /^(agent|tool):(.*)$/is

// Splits on whitespace, treating a double-quoted run as one chunk so
// `agent:"my agent"` and `"exact phrase"` survive intact.
function chunk(input) {
  const out = []
  let cur = ''
  let inQuotes = false
  for (const ch of input) {
    if (ch === '"') { inQuotes = !inQuotes; continue }
    if (!inQuotes && /\s/.test(ch)) {
      if (cur) out.push(cur)
      cur = ''
      continue
    }
    cur += ch
  }
  if (cur) out.push(cur)
  return out
}

// Returns the two query params the box can drive: { agent, search }. The UI
// renders its active-filter chips from these rather than from the tokens it
// recognised, so what is displayed cannot drift from what is requested.
export function parseAuditSearch(input) {
  let agent = ''
  const terms = []
  for (const c of chunk(input || '')) {
    const m = PREFIXED.exec(c)
    if (!m) { terms.push(c); continue }
    // A bare `agent:` is mid-typing, not a filter and not literal text.
    if (!m[2]) continue
    if (m[1].toLowerCase() === 'agent') {
      agent = m[2] // one ?agent= on the wire, so the last one typed wins
    } else {
      terms.push(m[2])
    }
  }
  return { agent, search: terms.join(' ') }
}
