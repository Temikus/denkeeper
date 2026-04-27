// Quintile breakpoints derived from the actual distribution of weekly token
// volumes. Cuts are weighted toward the top of the list so the head of the
// popularity-sorted dropdown visibly fans out (top 3% → 5 bars, 3-10% → 4,
// 10-30% → 3, 30-60% → 2, bottom 40% → 1).
const BREAKPOINTS = [
  { p: 0.97, bars: 5 },
  { p: 0.90, bars: 4 },
  { p: 0.70, bars: 3 },
  { p: 0.40, bars: 2 },
]

export function computeBreakpoints(models) {
  const vals = []
  for (const m of models) {
    const v = m.weekly_tokens || 0
    if (v > 0) vals.push(v)
  }
  if (!vals.length) return null
  vals.sort((a, b) => a - b)
  const at = (p) => vals[Math.min(Math.floor(p * vals.length), vals.length - 1)]
  return BREAKPOINTS.map(({ p, bars }) => ({ threshold: at(p), bars }))
}

export function popularityBars(v, breakpoints) {
  if (!v || !breakpoints) return 0
  for (const { threshold, bars } of breakpoints) {
    if (v >= threshold) return bars
  }
  return 1
}
