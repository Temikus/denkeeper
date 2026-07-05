// Stable per-agent identity color. Hashes the agent name to a hue so the
// same agent always renders the same color across pages and sessions.
// Fixed mid-range saturation/lightness reads on both light and dark themes.
export function agentColor(name) {
  let h = 0
  const s = name || ''
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return `hsl(${h % 360} 55% 45%)`
}
