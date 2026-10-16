// Display rules for the Tasks page, kept out of the components so they can be
// tested on plain data. Member IDs follow internal/task: "human" is you,
// "agent:<name>" a den agent, "ext:<key name>" an outside harness, "" nobody.

import { relativeTime } from './relativeTime.js'

export const STATUS_LABELS = {
  todo: 'To do',
  in_progress: 'In progress',
  review: 'Review',
  blocked: 'Blocked',
  done: 'Done',
  cancelled: 'Cancelled',
}

// Four fixed columns. Blocked stays with In progress and shows a pill, and
// Cancelled folds into Done (Paper "Tasks UI flow", stage 03).
export const COLUMNS = [
  { id: 'todo', label: 'To do' },
  { id: 'in_progress', label: 'In progress' },
  { id: 'review', label: 'Review' },
  { id: 'done', label: 'Done' },
]

export function columnOf(status) {
  switch (status) {
    case 'in_progress':
    case 'blocked':
      return 'in_progress'
    case 'review':
      return 'review'
    case 'done':
    case 'cancelled':
      return 'done'
    default:
      return 'todo'
  }
}

export function isClosed(status) {
  return status === 'done' || status === 'cancelled'
}

export function taskKey(id) {
  return `DK-${id}`
}

/** "You", "Unassigned", or the member's name without its prefix. */
export function memberName(id, members = []) {
  if (!id) return 'Unassigned'
  if (id === 'human') return 'You'
  const m = members.find(x => x.id === id)
  if (m) return m.name
  return id.replace(/^(agent|ext):/, '')
}

export function memberKind(id) {
  if (id === 'human') return 'human'
  if (id?.startsWith('agent:')) return 'agent'
  if (id?.startsWith('ext:')) return 'external'
  return ''
}

/**
 * Tasks that need you: blocked ones, ones in Review that you review, and ones
 * assigned to you. The same rule will drive the Overview list and nav badge.
 */
export function isWaitingOnYou(t) {
  if (isClosed(t.status)) return false
  if (t.status === 'blocked') return true
  if (t.status === 'review') return t.reviewer === 'human'
  return t.assignee === 'human'
}

/** Member picker groups, in the order the composer lists them. */
export function memberGroups(members = []) {
  return [
    { label: 'Den agents', members: members.filter(m => m.kind === 'agent') },
    { label: 'External', members: members.filter(m => m.kind === 'external') },
  ].filter(g => g.members.length > 0)
}

/**
 * What assigning to this member will do, in one line. Nothing wakes an agent:
 * a den agent finds work at its check-in, a harness when it next looks.
 * checkIns maps agent name to its check-in schedule; null means unknown.
 */
export function assignHint(id, members = [], checkIns = null) {
  if (!id) return 'Waits in To do. Anyone may claim it.'
  if (id === 'human') return 'Lands on your list.'
  const name = memberName(id, members)
  if (id.startsWith('agent:') && checkIns) {
    return checkIns.has(id.slice('agent:'.length))
      ? `${name} picks it up at its next check-in.`
      : `${name} doesn't check its tasks yet, so it won't pick this up.`
  }
  return `${name} picks it up when it next checks its tasks.`
}

/**
 * How long a claim has been held, e.g. "14m". A server clock ahead of the
 * browser's would otherwise read "in 2m", so a future time counts as now.
 */
export function heldFor(ts, now = Date.now()) {
  const t = new Date(ts).getTime()
  if (Number.isNaN(t)) return ''
  return relativeTime(Math.min(t, now), now).replace(/ ago$/, '')
}

/** The small text on the right of a board card. */
export function cardMeta(t, now = Date.now()) {
  if (t.status === 'blocked') return ''
  if (t.status === 'in_progress' && t.held_at) {
    return `held ${heldFor(t.held_at, now)}`
  }
  if (t.status === 'review') {
    if (t.reviewer === 'human') return 'your review'
    return t.reviewer ? `${memberName(t.reviewer)} reviews` : ''
  }
  if (t.status === 'todo') {
    if (!t.assignee) return 'anyone may claim'
    if (t.assignee === 'human') {
      return t.created_by && t.created_by !== 'human' ? `filed by ${memberName(t.created_by)}` : ''
    }
    return 'waits for check-in'
  }
  return ''
}

function parseData(data) {
  if (!data) return {}
  try {
    return JSON.parse(data) || {}
  } catch {
    return {}
  }
}

/** One line for a thread event that isn't a comment. */
export function eventLine(e, members = []) {
  const who = memberName(e.actor, members)
  const d = parseData(e.data)
  switch (e.kind) {
    case 'created':
      return d.assignee ? `${who} filed it for ${memberName(d.assignee, members)}` : `${who} filed it`
    case 'claimed':
      return `${who} claimed it`
    case 'released':
      return d.holder && d.holder !== e.actor
        ? `${who} released ${memberName(d.holder, members)}'s claim`
        : `${who} released it`
    case 'status':
      if (d.to === 'blocked') return `${who} marked it blocked${e.body ? `: ${e.body}` : ''}`
      return d.to ? `${who} moved it to ${STATUS_LABELS[d.to] || d.to}` : `${who} changed its status`
    case 'assigned':
      return d.to ? `${who} assigned it to ${memberName(d.to, members)}` : `${who} unassigned it`
    case 'subtask':
      return `${who} filed ${d.child_id ? taskKey(d.child_id) : 'a subtask'}: ${e.body}`
    case 'edited':
      return `${who} edited it`
    default:
      return `${who}: ${e.kind}`
  }
}
