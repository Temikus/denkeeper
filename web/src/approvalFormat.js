import { CHECK_LABELS, floorScore } from './deciderCalibration.js'

const DENIED = new Set(['supervisor_denied', 'supervisor_escalated', 'denied'])
const APPROVED = new Set(['supervisor_approved', 'approved'])

/**
 * The line a shadow decider's verdict adds to an approval card, or null.
 * A pending card shows the decider's lowest check as evidence. An answered
 * card shows only the two disagreements the calibration panel counts.
 */
export function shadowLine(appr) {
  const s = appr?.shadow
  if (!s) return null
  if (appr.status === 'pending') {
    if (!s.lowest || typeof s.score !== 'number') return null
    return { kind: 'evidence', text: `${s.decider} lowest check: ${CHECK_LABELS[s.lowest] || s.lowest} ${floorScore(s.score)}` }
  }
  let text = null
  if (s.verdict === 'APPROVE' && DENIED.has(appr.status)) text = `${s.decider} would have approved`
  if (s.verdict === 'DENY' && APPROVED.has(appr.status)) text = `${s.decider} would have denied`
  if (!text) return null
  const href = s.agent ? `#/agents/${encodeURIComponent(s.agent)}?card=permission` : null
  return { kind: 'disagree', text, href, decider: s.decider }
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
