// Threshold maths for the decision model calibration panel. Rows are the
// reviews from GET /agents/{name}/decider-reviews.

export const DECIDER_APPROVE_DEFAULT = 0.95
export const DECIDER_DENY_DEFAULT = 0.05

export const CHECK_LABELS = {
  aligned: 'aligned', scoped: 'scoped', no_injection: 'no injection', no_secrets: 'no secrets', no_leak: 'no leak',
  no_self_grant: 'no self-grant',
}

// Rounded down so a shown score never passes a threshold the real one misses.
export const floorScore = v => (Math.floor(v * 100 + 1e-9) / 100).toFixed(2)

// Fraction digits past the millisecond, padded so two in the same millisecond
// compare as text. RFC3339Nano trims trailing zeros, so raw text misorders them.
const subMilli = t => ((/\.(\d+)/.exec(t) || [])[1] || '').padEnd(9, '0').slice(3)

// Orders two RFC3339 times to the nanosecond: negative when a is earlier.
const compareTime = (a, b) => (new Date(a) - new Date(b)) || subMilli(a).localeCompare(subMilli(b))

// The server pairs a supervisor review up to 15 minutes after the shadow one.
const PAIR_WINDOW_MS = 15 * 60 * 1000

// A review newer than the mark, or any review before the first mark.
export function isFresh(row, reviewedUpTo) {
  return !reviewedUpTo || compareTime(row.time, reviewedUpTo) > 0
}

// The time to send as the new mark: the newest review in an unbroken run of
// settled ones, oldest first, so the mark never passes a review whose
// supervisor verdict can still arrive. A review is settled when it has a
// verdict, a failed supervisor review (supervisor_name and no verdict), or is
// older than the pairing window. Returns the row's own time string, never
// re-formatted: a Date would cut it to milliseconds. null when nothing is settled.
export function reviewedUpTo(rows, now = Date.now()) {
  const settled = row => !!row.supervisor || !!row.supervisor_name || now - new Date(row.time) > PAIR_WINDOW_MS
  const sorted = [...rows].sort((a, b) => compareTime(a.time, b.time))
  let last = null
  for (const row of sorted) {
    if (!settled(row)) break
    last = row.time
  }
  return last
}

// The lowest check that can deny. no_leak and no_self_grant only escalate,
// so the server leaves them out of min_deny_score; rows without the field
// fall back to min_score.
const denyScore = row => row.min_deny_score ?? row.min_score

// Mirrors decideSupervisorOutcome in internal/agent/supervisor_decider.go:
// deny is checked first, and a review with a missing answer always escalates.
export function verdict(row, approveAt, denyAt) {
  const min = row.min_score
  if (min === null || min === undefined) return 'ESCALATE'
  if (denyScore(row) <= denyAt) return 'DENY'
  if (min >= approveAt) return 'APPROVE'
  return 'ESCALATE'
}

// The decider approved a call the supervisor did not.
function unsafeApproval(row, v) {
  return v === 'APPROVE' && (row.supervisor === 'DENY' || row.supervisor === 'ESCALATE')
}

// The decider denied a call the supervisor approved.
function wrongDenial(row, v) {
  return v === 'DENY' && row.supervisor === 'APPROVE'
}

export function summarize(rows, approveAt, denyAt) {
  const s = {
    total: rows.length, settled: 0, escalated: 0, unsafeApprovals: 0, wrongDenials: 0,
    avoidedUSD: 0, costKnown: 0, deciderUSD: 0,
  }
  for (const row of rows) {
    const v = verdict(row, approveAt, denyAt)
    s.deciderUSD += row.decider_cost || 0
    if (v === 'ESCALATE') { s.escalated++; continue }
    s.settled++
    if (unsafeApproval(row, v)) s.unsafeApprovals++
    if (wrongDenial(row, v)) s.wrongDenials++
    if (row.supervisor_cost !== null && row.supervisor_cost !== undefined) {
      s.avoidedUSD += row.supervisor_cost
      s.costKnown++
    }
  }
  return s
}

// Bins of the lowest check score, each split by the supervisor's verdict.
// Reviews with a missing answer have no score to place and are left out.
export function histogram(rows, bins = 20) {
  const out = Array.from({ length: bins }, () => ({ APPROVE: 0, ESCALATE: 0, DENY: 0, none: 0, total: 0 }))
  for (const row of rows) {
    if (row.min_score === null || row.min_score === undefined) continue
    const i = Math.min(bins - 1, Math.max(0, Math.floor(row.min_score * bins)))
    const key = ['APPROVE', 'ESCALATE', 'DENY'].includes(row.supervisor) ? row.supervisor : 'none'
    out[i][key]++
    out[i].total++
  }
  return out
}

// Unsafe approvals first (a denial before an escalation), then wrong denials;
// newest first within each group.
export function disagreements(rows, approveAt, denyAt) {
  const rank = (row, v) => {
    if (v === 'APPROVE' && row.supervisor === 'DENY') return 0
    if (unsafeApproval(row, v)) return 1
    return 2
  }
  return rows
    .map(row => ({ row, v: verdict(row, approveAt, denyAt) }))
    .filter(({ row, v }) => unsafeApproval(row, v) || wrongDenial(row, v))
    .sort((a, b) => rank(a.row, a.v) - rank(b.row, b.v) || new Date(b.row.time) - new Date(a.row.time))
    .map(({ row, v }) => ({ ...row, decider_verdict: v }))
}

const roundUp2 = x => Math.floor(x * 100 + 1e-9) / 100 + 0.01
const roundDown2 = x => Math.ceil(x * 100 - 1e-9) / 100 - 0.01
const fix2 = x => Math.round(x * 100) / 100
const MAX_APPROVE = 0.99
const MIN_DENY = 0.01

// The nearest two-decimal threshold that removes the worse kind of
// disagreement: approve above every unsafe approval, else deny below every
// wrong denial. When that would need 1 (or 0), it falls back to 0.99 (or 0.01)
// if that removes some. Returns null when there is nothing to fix;
// {kind, count, value: null} when no threshold helps; otherwise
// {kind, value, count, removed, settledAfter}.
export function suggestion(rows, approveAt, denyAt) {
  const s = summarize(rows, approveAt, denyAt)
  if (s.unsafeApprovals > 0) {
    const scores = rows.filter(r => unsafeApproval(r, verdict(r, approveAt, denyAt))).map(r => r.min_score)
    const value = Math.min(fix2(roundUp2(Math.max(...scores))), MAX_APPROVE)
    const removed = scores.filter(x => x < value).length
    if (!removed || value <= approveAt) return { kind: 'approve', value: null, count: s.unsafeApprovals }
    return { kind: 'approve', value, count: s.unsafeApprovals, removed, settledAfter: summarize(rows, value, denyAt).settled }
  }
  if (s.wrongDenials > 0) {
    const scores = rows.filter(r => wrongDenial(r, verdict(r, approveAt, denyAt))).map(denyScore)
    const value = Math.max(fix2(roundDown2(Math.min(...scores))), MIN_DENY)
    const removed = scores.filter(x => x > value).length
    if (!removed || value >= denyAt) return { kind: 'deny', value: null, count: s.wrongDenials }
    return { kind: 'deny', value, count: s.wrongDenials, removed, settledAfter: summarize(rows, approveAt, value).settled }
  }
  return null
}
