// Compact relative + short absolute time formatting shared by dashboard pages.
// Both helpers take an optional `now` (ms epoch) so tests stay deterministic.

// "45m ago" / "in 2h 15m". Future values in the hours/days tiers carry a
// second unit because upcoming times (schedule runs) benefit from precision;
// past values stay single-unit like the audit views.
export function relativeTime(ts, now = Date.now()) {
  if (!ts) return ''
  const t = new Date(ts).getTime()
  if (Number.isNaN(t)) return ''
  const future = t > now
  const diff = Math.abs(t - now)
  const s = Math.floor(diff / 1000)
  const m = Math.floor(s / 60)
  const h = Math.floor(m / 60)
  const d = Math.floor(h / 24)
  let out
  if (s < 60) out = `${s}s`
  else if (m < 60) out = `${m}m`
  else if (h < 24) out = future && m % 60 ? `${h}h ${m % 60}m` : `${h}h`
  else out = future && h % 24 ? `${d}d ${h % 24}h` : `${d}d`
  return future ? `in ${out}` : `${out} ago`
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

// "today 14:30" / "tomorrow 09:00" / "yesterday 18:00" / "Mar 5 09:00".
// Fixed month names and 24h time keep output identical across runner locales.
export function shortAbsolute(ts, now = Date.now()) {
  if (!ts) return ''
  const dt = new Date(ts)
  if (Number.isNaN(dt.getTime())) return ''
  const pad = (n) => String(n).padStart(2, '0')
  const time = `${pad(dt.getHours())}:${pad(dt.getMinutes())}`
  const startOfDay = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const dayDiff = Math.round((startOfDay(dt) - startOfDay(new Date(now))) / 86400000)
  if (dayDiff === 0) return `today ${time}`
  if (dayDiff === 1) return `tomorrow ${time}`
  if (dayDiff === -1) return `yesterday ${time}`
  return `${MONTHS[dt.getMonth()]} ${dt.getDate()} ${time}`
}
