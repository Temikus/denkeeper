<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let schedules = []
  let loading = true
  let error = ''

  // Add/Edit modal
  let showModal = false
  let editingName = null // null = add mode, string = edit mode
  let formName = ''
  let formSchedule = ''
  let formSkill = ''
  let formChannel = ''
  let formSessionMode = 'isolated'
  let formSessionTier = ''
  let formAgent = 'default'
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
      schedules = (await api.schedules()) || []
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function openAdd() {
    editingName = null
    formName = ''
    formSchedule = ''
    formSkill = ''
    formChannel = ''
    formSessionMode = 'isolated'
    formSessionTier = ''
    formAgent = 'default'
    formTags = ''
    formEnabled = true
    showModal = true
  }

  function openEdit(s) {
    editingName = s.name
    formName = s.name
    formSchedule = s.expression
    formSkill = s.skill || ''
    formChannel = s.channel || ''
    formSessionMode = s.session_mode || 'isolated'
    formSessionTier = s.session_tier || ''
    formAgent = s.agent || 'default'
    formTags = (s.tags || []).join(', ')
    formEnabled = s.enabled
    showModal = true
  }

  async function saveSchedule() {
    saving = true
    error = ''
    try {
      const tags = formTags.trim() ? formTags.split(',').map(t => t.trim()).filter(Boolean) : []
      if (editingName) {
        // Update (PATCH)
        await api.updateSchedule(editingName, {
          schedule: formSchedule,
          skill: formSkill || undefined,
          channel: formChannel,
          session_mode: formSessionMode,
          session_tier: formSessionTier || undefined,
          tags: tags.length ? tags : undefined,
          enabled: formEnabled,
        })
      } else {
        // Create (POST)
        await api.addSchedule({
          name: formName.trim(),
          schedule: formSchedule,
          skill: formSkill || undefined,
          channel: formChannel,
          session_mode: formSessionMode,
          session_tier: formSessionTier || undefined,
          agent: formAgent || 'default',
          tags: tags.length ? tags : undefined,
          enabled: formEnabled,
        })
      }
      showModal = false
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
  <div class="header">
    <h1 class="page-title">Schedules</h1>
    <button class="btn-primary" onclick={openAdd}>+ Add Schedule</button>
  </div>

  <ErrorBanner message={error} />

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
          <tr>
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

<!-- Add/Edit Schedule Modal -->
{#if showModal}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) showModal = false }} role="dialog" aria-modal="true">
    <div class="modal wide">
      <h2>{editingName ? 'Edit Schedule' : 'Add Schedule'}</h2>
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
        <input type="text" bind:value={formChannel} placeholder="adapter:externalID (e.g. telegram:123456)" />
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
          Session Tier <span class="hint">(optional)</span>
          <select bind:value={formSessionTier}>
            <option value="">Default</option>
            <option value="autonomous">Autonomous</option>
            <option value="supervised">Supervised</option>
            <option value="restricted">Restricted</option>
          </select>
        </label>
      </div>
      {#if !editingName}
        <label>
          Agent
          <input type="text" bind:value={formAgent} placeholder="default" />
        </label>
      {/if}
      <label>
        Tags <span class="hint">(comma-separated)</span>
        <input type="text" bind:value={formTags} placeholder="e.g. reporting, daily" />
      </label>
      <label class="toggle-row">
        <input type="checkbox" bind:checked={formEnabled} />
        Enabled
      </label>
      <div class="modal-actions">
        <button class="btn-primary" onclick={saveSchedule}
          disabled={saving || !formName.trim() || !formSchedule.trim() || !formChannel.trim()}>
          {saving ? 'Saving...' : (editingName ? 'Update' : 'Add Schedule')}
        </button>
        <button class="btn-ghost" onclick={() => showModal = false}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- Delete Confirmation -->
{#if confirmDelete}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmDelete = null }} role="dialog" aria-modal="true">
    <div class="modal">
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
  .header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 20px; }
  .page-title { font-size: 20px; font-weight: 700; }
  .table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .table th, .table td { padding: 9px 10px; border-bottom: 1px solid var(--border); text-align: left; }
  .table th { color: var(--text-muted); font-size: 11px; text-transform: uppercase; font-weight: 500; white-space: nowrap; }
  .name { font-weight: 600; }
  .expr { font-family: monospace; font-size: 12px; white-space: nowrap; }
  .date { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
  .dot { display: inline-block; width: 7px; height: 7px; border-radius: 50%; background: var(--border); margin-right: 4px; vertical-align: middle; }
  .dot.on { background: var(--success); }
  .muted { color: var(--text-muted); font-size: 13px; }
  .actions { white-space: nowrap; }

  .btn-primary { background: var(--accent); color: #fff; border: none; padding: 8px 16px; border-radius: var(--radius); cursor: pointer; font-size: 13px; }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-ghost { background: none; border: 1px solid var(--border); color: var(--text); padding: 8px 16px; border-radius: var(--radius); cursor: pointer; font-size: 13px; }
  .btn-ghost:hover { border-color: var(--text-muted); }
  .btn-sm { background: var(--border); border: none; color: var(--text); padding: 4px 10px; border-radius: var(--radius); cursor: pointer; font-size: 12px; margin-right: 4px; }
  .btn-sm:hover { background: var(--accent); color: #fff; }
  .btn-sm.danger:hover { background: var(--danger); }
  .btn-danger { background: var(--danger); color: #fff; border: none; padding: 8px 16px; border-radius: var(--radius); cursor: pointer; font-size: 13px; }
  .btn-danger:hover:not(:disabled) { opacity: 0.85; }
  .btn-danger:disabled { opacity: 0.5; cursor: not-allowed; }

  .overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.6); display: flex; align-items: center; justify-content: center; z-index: 100; }
  .modal { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 28px; width: 460px; max-width: 90vw; }
  .modal.wide { width: 520px; }
  .modal h2 { font-size: 16px; font-weight: 600; margin-bottom: 16px; }
  .modal p { color: var(--text-muted); margin-bottom: 20px; line-height: 1.6; }
  .modal label { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; font-size: 13px; color: var(--text-muted); }
  .modal input[type="text"], .modal select { background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); color: var(--text); padding: 8px 12px; font-size: 14px; }
  .modal input[type="text"]:focus, .modal select:focus { outline: none; border-color: var(--accent); }
  .modal input[type="text"]:disabled { opacity: 0.5; cursor: not-allowed; }
  .modal-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 8px; }
  .hint { font-size: 11px; color: var(--text-muted); }
  .row { display: flex; gap: 16px; }
  .row label { flex: 1; }
  .toggle-row { flex-direction: row !important; align-items: center; gap: 8px !important; cursor: pointer; }
  .toggle-row input[type="checkbox"] { width: 16px; height: 16px; }
</style>
