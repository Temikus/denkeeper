// Task check-ins: a headless schedule that runs the check-tasks skill, so a
// den agent finds work assigned to it without a chat. Built from the existing
// skill and schedule endpoints; nothing here is task-specific on the server.

import { api } from './api.js'

export const CHECK_IN_SKILL = 'check-tasks'
export const CHECK_IN_EVERY = '@every 30m'
export const CHECK_IN_LABEL = 'every 30 min'

export function checkInScheduleName(agent) {
  return `${CHECK_IN_SKILL}-${agent}`
}

const SKILL_BODY = `You're checking your task list. This runs on a schedule with no chat, so nobody reads your reply: the task thread is where you report.

1. Call task_list with no arguments to get your open tasks.
2. If none are To do or In progress, reply "No tasks." and stop.
3. Pick one: an In progress task you already hold, else the oldest To do.
4. Claim it with task_checkout. If the claim is refused, don't retry. Move to the next task, or stop.
5. Read the thread with task_get, then do the work.
6. Post what you did, and anything you need from the filer, with task_comment.
7. Finish with task_update: review when it has a reviewer, done when it doesn't, or blocked with a reason when you're stuck.

Work on one task per check-in. Leave unassigned tasks alone. Reply with one line naming the task and where you left it.`

export const CHECK_IN_SKILL_DEF = {
  name: CHECK_IN_SKILL,
  description: 'Check my task list and work on one task',
  version: '1',
  // A command trigger keeps the skill out of ordinary chat turns (a skill with
  // no trigger matches every message) and lets you run it with /check-tasks.
  triggers: [`command:${CHECK_IN_SKILL}`],
  body: SKILL_BODY,
  max_tool_rounds: 25,
  // No requires_tools: declaring tools narrows the turn to only those, and the
  // agent needs its whole tool set to do the work a task asks for.
}

/** Agent name from a member ID, or '' for anyone who isn't a den agent. */
export function agentOf(memberID) {
  return memberID?.startsWith('agent:') ? memberID.slice('agent:'.length) : ''
}

/**
 * Map of agent name to its check-in schedule, from GET /schedules. null means
 * the schedules couldn't be read, which is not the same as "none set up".
 */
export async function loadCheckIns() {
  try {
    const schedules = await api.schedules()
    const map = new Map()
    for (const s of schedules || []) {
      if (s.skill === CHECK_IN_SKILL && s.agent) map.set(s.agent, s)
    }
    return map
  } catch {
    return null
  }
}

/** Creates the skill if the agent lacks it, then the headless schedule. */
export async function setUpCheckIn(agent) {
  try {
    await api.getSkill(agent, CHECK_IN_SKILL)
  } catch (e) {
    if (e.status !== 404) throw e
    await api.addSkill(agent, CHECK_IN_SKILL_DEF)
  }
  await api.addSchedule({
    name: checkInScheduleName(agent),
    schedule: CHECK_IN_EVERY,
    skill: CHECK_IN_SKILL,
    agent,
    session_mode: 'isolated',
    headless: true,
  })
}
