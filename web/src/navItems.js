// The one list of dashboard pages. The sidebar, the mobile More page and the
// top-bar breadcrumb all read it, so a page added here shows up everywhere.

export const topLinks = [
  { id: 'overview', label: 'Overview' },
  { id: 'chat',     label: 'Chat' },
]

export const sections = [
  {
    id: 'agents',
    label: 'Agents',
    items: [
      { id: 'agents',    label: 'Agents' },
      { id: 'sessions',  label: 'Sessions' },
      { id: 'channels',  label: 'Channels' },
      { id: 'schedules', label: 'Schedules' },
      { id: 'approvals', label: 'Approvals' },
      { id: 'audit',     label: 'Audit Log' },
      { id: 'traces',    label: 'Turn inspector' },
    ],
  },
  {
    id: 'platform',
    label: 'Platform',
    items: [
      { id: 'skills',  label: 'Skills' },
      { id: 'tools',   label: 'Tools' },
      { id: 'browser', label: 'Browser' },
      { id: 'kv',      label: 'KV Store' },
    ],
  },
  {
    id: 'admin',
    label: 'Admin',
    items: [
      { id: 'server',    label: 'Server' },
      { id: 'providers', label: 'Providers' },
      { id: 'costs',     label: 'Costs' },
      { id: 'evals',     label: 'Evals' },
      { id: 'keys',      label: 'API Keys' },
      { id: 'settings',  label: 'Settings' },
    ],
  },
]

/** Returns { section, item } for a top-level route; section is null for top links. */
export function findItem(route) {
  const id = route || 'overview'
  const top = topLinks.find(l => l.id === id)
  if (top) return { section: null, item: top }
  for (const section of sections) {
    const item = section.items.find(i => i.id === id)
    if (item) return { section, item }
  }
  if (id === 'more') return { section: null, item: { id: 'more', label: 'More' } }
  return { section: null, item: null }
}
