<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let schedules = []
  let loading = true
  let error = ''
  let timezone = 'UTC'

  // Available channels and agents for dropdowns
  let channels = []
  let agents = []
  let loadWarning = ''

  // Inline add/edit panel
  let showForm = false
  let editingName = null // null = add mode, string = edit mode
  let formName = ''
  let formSchedule = ''
  let formSkill = ''
  let formChannel = ''
  let formChannelCustom = ''
  let formSessionMode = 'isolated'
  let formSessionTier = ''
  let formAgent = ''
  let formAgentCustom = ''
  let formTags = ''
  let formEnabled = true
  let saving = false

  // Delete confirmation
  let confirmDelete = null
  let deleting = false

  async function loadData() {
    loading = true
    error = ''
    try {
      let chErr = false, agErr = false
      const [sched, cfg, ch, ag] = await Promise.all([
        api.schedules(),
        api.serverConfig(),
        api.channels().catch(() => { chErr = true; return [] }),
        api.agents().catch(() => { agErr = true; return [] }),
      ])
      schedules = sched || []
      timezone = cfg?.timezone || 'UTC'
      channels = (ch || []).filter(c => !c.implicit)
      agents = ag || []
      const warnings = []
      if (chErr) warnings.push('channels')
      if (agErr) warnings.push('agents')
      loadWarning = warnings.length ? `Could not load ${warnings.join(' and ')} — use Custom entry instead.` : ''
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function resolvedChannel() {
    return formChannel === '__custom__' ? formChannelCustom : formChannel
  }

  function resolvedAgent() {
    return formAgent === '__custom__' ? formAgentCustom : (formAgent || 'default')
  }

  function openAdd() {
    editingName = null
    formName = ''
    formSchedule = ''
    formSkill = ''
    formChannel = channels.length ? channels[0].name : '__custom__'
    formChannelCustom = ''
    formSessionMode = 'isolated'
    formSessionTier = ''
    formAgent = agents.length ? agents[0].name : '__custom__'
    formAgentCustom = ''
    formTags = ''
    formEnabled = true
    showForm = true
  }

  function openEdit(s) {
    editingName = s.name
    formName = s.name
    formSchedule = s.expression
    formSkill = s.skill || ''
    const knownChannel = channels.find(c => c.name === s.channel)
    formChannel = knownChannel ? s.channel : '__custom__'
    formChannelCustom = knownChannel ? '' : (s.channel || '')
    formSessionMode = s.session_mode || 'isolated'
    formSessionTier = s.session_tier || ''
    const knownAgent = agents.find(a => a.name === s.agent)
    formAgent = knownAgent ? s.agent : '__custom__'
    formAgentCustom = formAgent === '__custom__' ? (s.agent || '') : ''
    formTags = (s.tags || []).join(', ')
    formEnabled = s.enabled
    showForm = true
  }

  function closeForm() {
    showForm = false
  }

  async function saveSchedule() {
    saving = true
    error = ''
    try {
      const tags = formTags.trim() ? formTags.split(',').map(t => t.trim()).filter(Boolean) : []
      const channel = resolvedChannel()
      const agent = resolvedAgent()
      if (editingName) {
        await api.updateSchedule(editingName, {
          schedule: formSchedule,
          skill: formSkill || undefined,
          channel,
          session_mode: formSessionMode,
          session_tier: formSessionTier || undefined,
          agent: agent || undefined,
          tags: tags.length ? tags : undefined,
          enabled: formEnabled,
        })
      } else {
        await api.addSchedule({
          name: formName.trim(),
          schedule: formSchedule,
          skill: formSkill || undefined,
          channel,
          session_mode: formSessionMode,
          session_tier: formSessionTier || undefined,
          agent: agent || 'default',
          tags: tags.length ? tags : undefined,
          enabled: formEnabled,
        })
      }
      showForm = false
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  async function doDelete() {
    if (!confirmDelete) return
    deleting = true
    error = ''
    try {
      await api.deleteSchedule(confirmDelete)
      confirmDelete = null
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      deleting = false
    }
  }

  function fmtDate(s) { return s ? new Date(s).toLocaleString() : '—' }

  onMount(loadData)
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">Schedules</h1>
    <button class="btn-primary" onclick={openAdd} data-testid="add-schedule-btn">+ Add Schedule</button>
  </div>

  <p class="tz-note">Cron schedules run in <strong>{timezone}</strong> time. <a href="#/server">Change</a></p>

  <ErrorBanner message={error} />

  {#if loadWarning}
    <p class="load-warning" data-testid="load-warning">{loadWarning}</p>
  {/if}

  <!-- Inline Add/Edit Panel -->
  <div class="inline-panel" class:open={showForm}>
    <div class="inline-panel-inner">
      <div class="inline-form" data-testid="schedule-form">
        <h2 class="form-title">{editingName ? 'Edit Schedule' : 'Add Schedule'}</h2>
        <label>
          Name
          <input type="text" bind:value={formName} placeholder="e.g. daily-report" disabled={!!editingName} />
        </label>
        <label>
          Schedule Expression
          <input type="text" bind:value={formSchedule} placeholder="@daily, @every 5m, or 0 8 * * 1-5" />
          <span class="hint">@daily, @hourly, @every 5m, or 5-field cron</span>
        </label>
        <label>
          Skill <span class="hint">(optional)</span>
          <input type="text" bind:value={formSkill} placeholder="Skill name to invoke" />
        </label>
        <label>
          Channel
          <select bind:value={formChannel} disabled={saving} data-testid="channel-select">
            {#each channels as ch}
              <option value={ch.name}>{ch.name} ({ch.agent})</option>
            {/each}
            <option value="__custom__">Custom...</option>
          </select>
          {#if formChannel === '__custom__'}
            <input type="text" bind:value={formChannelCustom} placeholder="channel name" style="margin-top: 6px" />
          {/if}
        </label>
        <div class="row">
          <label>
            Session Mode
            <select bind:value={formSessionMode}>
              <option value="isolated">Isolated</option>
              <option value="shared">Shared</option>
            </select>
          </label>
          <label>
            <span style="white-space: nowrap">Session Tier <span class="hint">(optional)</span></span>
            <select bind:value={formSessionTier}>
              <option value="">Default</option>
              <option value="autonomous">Autonomous</option>
              <option value="supervised">Supervised</option>
              <option value="restricted">Restricted</option>
            </select>
          </label>
        </div>
        <label>
          Agent
          <select bind:value={formAgent} disabled={saving} data-testid="agent-select">
            {#each agents as a}
              <option value={a.name}>{a.name}</option>
            {/each}
            <option value="__custom__">Custom...</option>
          </select>
          {#if formAgent === '__custom__'}
            <input type="text" bind:value={formAgentCustom} placeholder="agent name" style="margin-top: 6px" />
          {/if}
        </label>
        <label>
          Tags <span class="hint">(comma-separated)</span>
          <input type="text" bind:value={formTags} placeholder="e.g. reporting, daily" />
        </label>
        <label class="toggle-row">
          <input type="checkbox" bind:checked={formEnabled} />
          Enabled
        </label>
        <div class="form-actions">
          <button class="btn-primary" onclick={saveSchedule}
            disabled={saving || !formName.trim() || !formSchedule.trim() || !resolvedChannel().trim()}>
            {saving ? 'Saving...' : (editingName ? 'Update' : 'Add Schedule')}
          </button>
          <button class="btn-ghost" onclick={closeForm}>Cancel</button>
        </div>
      </div>
    </div>
  </div>

  {#if loading}
    <p class="muted">Loading...</p>
  {:else if schedules.length === 0 && !error}
    <p class="muted">No schedules configured. Add one to automate recurring tasks.</p>
  {:else}
    <table class="table">
      <thead>
        <tr>
          <th>Name</th><th>Expression</th><th>Skill</th>
          <th>Channel</th><th>Tier</th><th>Enabled</th>
          <th>Last Run</th><th>Next Run</th><th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each schedules as s}
          <tr data-testid="schedule-row-{s.name}">
            <td class="name">{s.name}</td>
            <td class="expr">{s.expression}</td>
            <td>{s.skill || '—'}</td>
            <td class="expr">{s.channel || '—'}</td>
            <td>{s.session_tier || '—'}</td>
            <td>
              <span class="dot" class:on={s.enabled}></span>
              {s.enabled ? 'yes' : 'no'}
            </td>
            <td class="date">{fmtDate(s.last_run)}</td>
            <td class="date">{fmtDate(s.next_run)}</td>
            <td class="actions">
              <button class="btn-sm" onclick={() => openEdit(s)}>Edit</button>
              <button class="btn-sm danger" onclick={() => { confirmDelete = s.name }}>Delete</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<!-- Delete Confirmation -->
{#if confirmDelete}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmDelete = null }} role="dialog" aria-modal="true">
    <div class="confirm-modal" data-testid="delete-confirm">
      <h2>Delete Schedule</h2>
      <p>Delete <strong>{confirmDelete}</strong>? This will stop the schedule and remove it from the configuration.</p>
      <div class="modal-actions">
        <button class="btn-danger" onclick={doDelete} disabled={deleting}>
          {deleting ? 'Deleting...' : 'Delete'}
        </button>
        <button class="btn-ghost" onclick={() => confirmDelete = null}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page { max-width: 1100px; }
  .tz-note { font-size: 12px; color: var(--text-muted); margin: -8px 0 12px; }
  .tz-note a { color: var(--accent); text-decoration: none; }
  .tz-note a:hover { text-decoration: underline; }
  .form-title { font-size: 16px; font-weight: 600; margin-bottom: 16px; }
  .name { font-weight: 600; }
  .expr { font-family: monospace; font-size: 12px; white-space: nowrap; }
  .date { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
  .dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; background: var(--border); margin-right: 4px; vertical-align: middle; }
  .dot.on { background: var(--success); }
  .actions { white-space: nowrap; }
  .load-warning { font-size: 12px; color: var(--text-muted); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; padding: 8px 12px; margin-bottom: 12px; }
</style>
