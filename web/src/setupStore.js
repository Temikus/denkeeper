import { writable, derived } from 'svelte/store'
import { api } from './api.js'

// Setup wizard progress, read from GET /onboarding. The server derives it
// from config, so it survives a new browser; nothing here is persisted.
export const setup = writable({
  loaded: false,
  completed: false,
  skipped: false,
  agent: '',
  steps: [],
  doneCount: 0,
  total: 4,
  restartRequired: false,
  restart: { available: false, managed: false },
})

// True while the full-screen wizard is showing.
export const wizardOpen = writable(false)

// The three steps a working agent needs. chat_app is optional.
const REQUIRED = ['provider', 'agent', 'persona']

// The first step not done yet, or '' when everything is.
export function nextStep(steps) {
  const open = steps.find(s => !s.done && !s.optional)
  return open ? open.id : ''
}

// Show the "Setup N of 4" reminders after a skip until the required steps
// are done. They stay even when the Overview card is hidden.
export const showSetupReminder = derived(setup, $s =>
  $s.loaded && $s.skipped && $s.steps.filter(s => REQUIRED.includes(s.id) && s.done).length < REQUIRED.length,
)

function fromResponse(ob) {
  const w = ob?.wizard || {}
  return {
    loaded: true,
    completed: !!(w.completed ?? ob?.wizard_completed),
    skipped: !!w.skipped,
    agent: w.agent || '',
    steps: w.steps || [],
    doneCount: w.done_count || 0,
    total: w.total || 4,
    restartRequired: !!w.restart_required,
    restart: w.restart || { available: false, managed: false },
  }
}

// refreshSetup reloads progress. It resolves to the new state, or null when
// the caller cannot read onboarding (e.g. a key without the admin scope).
export async function refreshSetup() {
  try {
    const state = fromResponse(await api.onboarding())
    setup.set(state)
    return state
  } catch {
    return null
  }
}

// skipSetup leaves the wizard for later. Errors propagate so the caller can
// show them; the wizard stays open if the server did not record the skip.
export async function skipSetup() {
  await api.wizardSkip()
  wizardOpen.set(false)
  await refreshSetup()
}

export async function completeSetup() {
  await api.wizardComplete()
  wizardOpen.set(false)
  await refreshSetup()
}

export function openWizard() {
  wizardOpen.set(true)
}
