import { CHECK_LABELS, floorScore } from './deciderCalibration.js'

const DENIED = new Set(['supervisor_denied', 'supervisor_escalated', 'denied'])
const APPROVED = new Set(['supervisor_approved', 'approved'])
const HUMAN_ANSWERED = new Set(['approved', 'denied'])

// " · <prefix><label> <score>", or "" when the decider gave no score.
function scorePart(s, prefix) {
  if (!s.lowest || typeof s.score !== 'number') return ''
  return ` · ${prefix}${CHECK_LABELS[s.lowest] || s.lowest} ${floorScore(s.score)}`
}

function pendingVerdict(s) {
  if (s.verdict === 'APPROVE') return { verdict: `${s.decider} would approve`, tone: 'approve' }
  if (s.verdict === 'DENY') return { verdict: `${s.decider} would deny`, tone: 'deny' }
  return { verdict: `${s.decider} is unsure`, tone: null }
}

function disagreement(s, status) {
  if (s.verdict === 'APPROVE' && DENIED.has(status)) return { verdict: `${s.decider} would have approved`, tone: 'approve' }
  if (s.verdict === 'DENY' && APPROVED.has(status)) return { verdict: `${s.decider} would have denied`, tone: 'deny' }
  return null
}

/**
 * The line a shadow decider's verdict adds to an approval card, or null.
 * `verdict` (coloured by `tone`) leads and `detail` follows it. A pending card
 * shows what the decider would do; an answered card shows only the two
 * disagreements the calibration panel counts. Only shadow mode sets `shadow`,
 * so the clause in `detail` is always true.
 * `siblings` are the message's other cards: a human answer to an escalated
 * call has no line, since the escalated card already shows it.
 */
export function shadowLine(appr, siblings = []) {
  const s = appr?.shadow
  if (!s) return null
  if (appr.status === 'pending') {
    return { kind: 'evidence', ...pendingVerdict(s), detail: `${scorePart(s, 'lowest: ')} · shadow mode, so you still decide` }
  }
  const d = disagreement(s, appr.status)
  if (!d) return null
  if (HUMAN_ANSWERED.has(appr.status) && appr.tool_id &&
      siblings.some(o => o !== appr && o.status === 'supervisor_escalated' && o.tool_id === appr.tool_id)) return null
  const href = s.agent ? `#/agents/${encodeURIComponent(s.agent)}?card=permission` : null
  return { kind: 'disagree', ...d, detail: `${scorePart(s, '')} · shadow mode, so this had no effect`, href, decider: s.decider }
}

// Engine summaries look like `[retry 1/2] Execute tool "name" with args: {...}`
// (internal/agent/engine.go); the raw args JSON is also sent as the payload.
const SUMMARY_RE = /^(?:\[retry (\d+)\/(\d+)\] )?Execute tool "([^"]+)" with args: ([\s\S]*)$/

// Strings longer than this, or with a newline, render as a block.
const INLINE_MAX = 80

/**
 * Split a tool-call approval into its parts for display.
 * Returns null when the summary is not an engine tool-call summary.
 * `args` is an array of {key, value, block}; it is null when the args are
 * not a JSON object, in which case `raw` holds them verbatim.
 */
export function parseToolCall(approval) {
  const m = SUMMARY_RE.exec(approval?.summary || '')
  if (!m) return null
  const raw = (approval.payload || m[4] || '').trim()
  const retry = m[1] ? `retry ${m[1]}/${m[2]}` : ''
  return { tool: m[3], retry, raw, args: parseArgs(raw) }
}

function parseArgs(raw) {
  if (!raw) return []
  let obj
  try { obj = JSON.parse(raw) } catch { return null }
  if (obj === null || typeof obj !== 'object' || Array.isArray(obj)) return null
  return Object.entries(obj).map(([key, v]) => formatValue(key, v))
}

function formatValue(key, v) {
  if (typeof v === 'string') {
    return { key, value: v, block: v.includes('\n') || v.length > INLINE_MAX }
  }
  if (v !== null && typeof v === 'object') {
    return { key, value: JSON.stringify(v, null, 2), block: true }
  }
  return { key, value: JSON.stringify(v), block: false }
}
