<script>
  import { onMount, tick } from 'svelte'
  import { api } from '../api.js'
  import { isMobile } from '../store.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import KebabMenu from '../components/KebabMenu.svelte'
  import { agentColor } from '../agentColor.js'
  import { relativeTime, shortAbsolute } from '../relativeTime.js'

  let schedules = $state([])
  let loading = $state(true)
  let error = $state('')
  let timezone = $state('UTC')

  // Available channels and agents for dropdowns
  let channels = $state([])
  let agents = $state([])
  let loadWarning = $state('')

  // Client-side agent filter: '' = all agents. The full list is always
  // loaded (chip counts and grouping need it); chips narrow which sections
  // render without another round-trip.
  let filterAgent = $state('')
  let hasSchedules = $state(false)

  // Inline add/edit panel
  let showForm = $state(false)
  let editingName = $state(null) // null = add mode, string = edit mode
  let formName = $state('')
  let formSchedule = $state('')
  let formSkill = $state('')
  let formChannel = $state('')
  let formChannelCustom = $state('')
  let formSessionMode = $state('isolated')
  let formSessionTier = $state('')
  let formAgent = $state('')
  let formAgentCustom = $state('')
  let formTags = $state('')
  let formEnabled = $state(true)
  let saving = $state(false)

  // Delete confirmation
  let confirmDelete = $state(null)
  let deleting = $state(false)

  // In-flight enable/disable toggle (schedule name)
  let togglingName = $state(null)

  let panelEl = $state(null)

  // The inline panel sits above the sections; on mobile, opening it from a
  // card deep in the list would otherwise expand off-screen with no feedback.
  function revealForm() {
    showForm = true
    if ($isMobile) tick().then(() => panelEl?.scrollIntoView?.({ behavior: 'smooth', block: 'start' }))
  }

  const tierByAgent = $derived(new Map(agents.map(a => [a.name, a.permission_tier])))

  // Schedules grouped by owning agent, sorted by agent name. The section
  // tier badge is the agent-level tier; a per-schedule session_tier renders
  // as a row-level override badge instead.
  const groups = $derived.by(() => {
    const m = new Map()
    for (const s of schedules) {
      const a = s.agent || 'default'
      if (!m.has(a)) m.set(a, [])
      m.get(a).push(s)
    }
    return [...m.entries()]
      .map(([agent, list]) => ({ agent, schedules: list, tier: tierByAgent.get(agent) }))
      .sort((a, b) => a.agent.localeCompare(b.agent))
  })

  const visibleGroups = $derived(filterAgent ? groups.filter(g => g.agent === filterAgent) : groups)

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
      filterAgent = ''
      hasSchedules = schedules.length > 0
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

  async function toggleEnabled(s) {
    togglingName = s.name
    error = ''
    try {
      await api.updateSchedule(s.name, { enabled: !s.enabled })
      // Light refetch for the server-computed next_run; keeps the client
      // filter and avoids flashing the page-level loading state.
      schedules = (await api.schedules()) || []
    } catch (e) {
      error = e.message
    } finally {
      togglingName = null
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
    revealForm()
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
    revealForm()
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

  onMount(loadData)
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">Schedules</h1>
    <button class="btn-primary" class:mobile-fab={$isMobile} onclick={openAdd}
      data-testid="add-schedule-btn" aria-label="Add schedule">{$isMobile ? '+' : '+ Add Schedule'}</button>
  </div>

  <p class="tz-note">Cron schedules run in <strong>{timezone}</strong> time. <a href="#/server">Change</a></p>

  <ErrorBanner message={error} />

  {#if loadWarning}
    <p class="load-warning" data-testid="load-warning">{loadWarning}</p>
  {/if}

  <!-- Inline Add/Edit Panel -->
  <div class="inline-panel" class:open={showForm} bind:this={panelEl}>
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
          <button class="btn-primary" onclick={saveSchedule} data-testid="schedule-save-btn"
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
  {:else if !hasSchedules && !error}
    <p class="muted">No schedules configured. Add one to automate recurring tasks.</p>
  {:else}
    {#if groups.length > 1}
      <div class="filter-chips" role="radiogroup" aria-label="Filter by agent" data-testid="agent-filter">
        <button class="chip" class:active={filterAgent === ''} role="radio" aria-checked={filterAgent === ''}
          onclick={() => filterAgent = ''}>All agents ({schedules.length})</button>
        {#each groups as g (g.agent)}
          <button class="chip" class:active={filterAgent === g.agent} role="radio" aria-checked={filterAgent === g.agent}
            data-testid="agent-chip-{g.agent}" onclick={() => filterAgent = g.agent}>
            <span class="chip-dot" style="background: {agentColor(g.agent)}"></span>{g.agent} ({g.schedules.length})
          </button>
        {/each}
      </div>
    {/if}

    {#each visibleGroups as g (g.agent)}
      <section class="agent-section" data-testid="agent-section-{g.agent}">
        <header class="section-header">
          <span class="agent-dot" style="background: {agentColor(g.agent)}"></span>
          <h2 class="agent-name">{g.agent}</h2>
          {#if g.tier}
            <span class="tier-badge tier-{g.tier}">{g.tier}</span>
          {/if}
          <span class="count">{$isMobile ? g.schedules.length : `${g.schedules.length} schedule${g.schedules.length === 1 ? '' : 's'}`}</span>
        </header>
        {#if $isMobile}
          <ul class="cell-list">
            {#each g.schedules as s (s.name)}
              <li class="cell" class:paused={!s.enabled} data-testid="schedule-row-{s.name}">
                <div class="cell-top">
                  <span class="name">{s.name}</span>
                  {#if s.session_tier}
                    <span class="tier-badge tier-{s.session_tier}">{s.session_tier}</span>
                  {/if}
                  <label class="switch">
                    <input type="checkbox" checked={s.enabled} disabled={togglingName === s.name}
                      onchange={() => toggleEnabled(s)} aria-label="Toggle {s.name}" />
                    <span class="switch-slider"></span>
                  </label>
                  <KebabMenu items={[
                    { label: 'Edit', onclick: () => openEdit(s) },
                    { label: 'Delete', danger: true, onclick: () => { confirmDelete = s.name } },
                  ]} />
                </div>
                {#if s.skill}
                  <div class="subline">skill: {s.skill}</div>
                {/if}
                <div class="cell-bottom">
                  <span class="cron">{s.expression}</span>
                  {#if !s.enabled}
                    <span class="paused-label">Paused</span>
                  {:else if s.next_run}
                    <span class="rel">{relativeTime(s.next_run)}</span>
                    <span class="abs">{shortAbsolute(s.next_run)}</span>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        {:else}
        <div class="table-wrap">
          <table class="table">
            <thead>
              <tr>
                <th>Name</th><th>Schedule</th><th>Channel</th>
                <th>Last run</th><th>Next run</th><th>On</th><th></th>
              </tr>
            </thead>
            <tbody>
              {#each g.schedules as s (s.name)}
                <tr data-testid="schedule-row-{s.name}" class:paused={!s.enabled}>
                  <td class="name-cell">
                    <span class="name">{s.name}</span>
                    {#if s.session_tier}
                      <span class="tier-badge tier-{s.session_tier}">{s.session_tier}</span>
                    {/if}
                    {#if s.skill}
                      <span class="subline">skill: {s.skill}</span>
                    {/if}
                  </td>
                  <td class="expr cron">{s.expression}</td>
                  <td class="expr channel">{s.channel || '—'}</td>
                  <td class="date">{s.last_run ? relativeTime(s.last_run) : 'Never'}</td>
                  <td class="date">
                    {#if !s.enabled}
                      <span class="paused-label">Paused</span>
                    {:else if s.next_run}
                      <span class="rel">{relativeTime(s.next_run)}</span>
                      <span class="abs">{shortAbsolute(s.next_run)}</span>
                    {:else}
                      —
                    {/if}
                  </td>
                  <td>
                    <label class="switch">
                      <input type="checkbox" checked={s.enabled} disabled={togglingName === s.name}
                        onchange={() => toggleEnabled(s)} aria-label="Toggle {s.name}" />
                      <span class="switch-slider"></span>
                    </label>
                  </td>
                  <td class="actions">
                    <button class="icon-btn" onclick={() => openEdit(s)} aria-label="Edit {s.name}" title="Edit">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5L17 3z"/></svg>
                    </button>
                    <button class="icon-btn danger" onclick={() => { confirmDelete = s.name }} aria-label="Delete {s.name}" title="Delete">
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        {/if}
      </section>
    {/each}
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
  .load-warning { font-size: 12px; color: var(--text-muted); background: var(--surface); border: 1px solid var(--border); border-radius: 6px; padding: 8px 12px; margin-bottom: 12px; }

  /* Agent filter chips */
  .filter-chips { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 16px; }
  .chip {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 4px 11px; background: transparent;
    border: 1px solid var(--border); border-radius: 999px;
    font-size: 12px; color: var(--text); cursor: pointer; transition: all 0.1s;
  }
  .chip:hover { border-color: var(--accent); }
  .chip:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .chip.active { background: var(--accent); color: white; border-color: var(--accent); }
  .chip-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }

  /* Agent sections */
  .agent-section {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius); margin-bottom: 20px; overflow: hidden;
  }
  .section-header {
    display: flex; align-items: center; gap: 8px;
    padding: 12px 16px; border-bottom: 1px solid var(--border);
  }
  .agent-dot { width: 10px; height: 10px; border-radius: 50%; flex-shrink: 0; }
  .agent-name { font-size: 14px; font-weight: 600; margin: 0; }
  .count { color: var(--text-muted); font-size: 12px; margin-left: auto; }
  .table-wrap { overflow-x: auto; }
  .table-wrap .table { margin-bottom: 0; }

  /* Tier badge (matches Agents page) */
  .tier-badge {
    display: inline-block; padding: 2px 8px; border-radius: 4px;
    font-size: 11px; font-weight: 500; text-transform: capitalize;
  }
  .tier-autonomous { background: rgba(76,175,125,0.15); color: var(--success); }
  .tier-supervised { background: rgba(240,169,88,0.15); color: var(--warn); }
  .tier-restricted { background: rgba(224,92,110,0.15); color: var(--danger); }

  /* Rows */
  .name-cell .name { font-weight: 600; }
  .name-cell .tier-badge { margin-left: 6px; }
  .name-cell .subline { display: block; color: var(--text-muted); font-size: 11px; margin-top: 2px; }
  .expr { font-family: monospace; font-size: 12px; white-space: nowrap; }
  .channel { color: var(--text-muted); }
  .date { color: var(--text-muted); font-size: 12px; white-space: nowrap; }
  .rel { color: var(--text); font-weight: 500; }
  .abs { display: block; color: var(--text-muted); font-size: 11px; }
  .paused-label { color: var(--text-muted); }
  tr.paused .name, tr.paused .cron,
  .cell.paused .name, .cell.paused .cron { opacity: 0.55; }

  /* Round add-FAB when the header button renders in mobile mode */
  .mobile-fab {
    width: 36px; height: 36px; border-radius: 50%;
    padding: 0; display: flex; align-items: center; justify-content: center;
    font-size: 20px; line-height: 1;
  }

  /* Mobile card list — replaces the per-section table under isMobile */
  .cell-list { list-style: none; margin: 0; padding: 0; }
  .cell { display: flex; flex-direction: column; gap: 4px; padding: 12px 16px; }
  .cell + .cell { border-top: 1px solid var(--border); }
  .cell-top { display: flex; align-items: center; gap: 8px; }
  .cell-top .name {
    flex: 1; min-width: 0; font-weight: 600; font-size: 13px;
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .cell .subline { color: var(--text-muted); font-size: 11px; }
  .cell-bottom { display: flex; align-items: baseline; gap: 8px; font-size: 12px; }
  .cell-bottom .cron {
    flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis;
    font-family: monospace;
  }
  .cell-bottom .rel { font-size: 11px; }
  .cell-bottom .abs { display: inline; white-space: nowrap; }
  .cell-bottom .paused-label { font-size: 11px; }

  /* Pill toggle switch — Tools-style, compact for table rows; green = enabled status */
  .switch { position: relative; display: inline-block; width: 36px; height: 20px; flex-shrink: 0; }
  .switch input { opacity: 0; width: 0; height: 0; }
  .switch-slider {
    position: absolute; cursor: pointer; inset: 0;
    background: var(--border); border-radius: 20px; transition: background 0.2s;
  }
  .switch-slider::before {
    content: ""; position: absolute; height: 14px; width: 14px;
    left: 3px; bottom: 3px; background: white; border-radius: 50%;
    transition: transform 0.2s;
  }
  .switch input:checked + .switch-slider { background: var(--success); }
  .switch input:checked + .switch-slider::before { transform: translateX(16px); }
  .switch input:disabled + .switch-slider { opacity: 0.6; cursor: wait; }
  .switch input:focus-visible + .switch-slider { outline: 2px solid var(--accent); outline-offset: 2px; }

  /* Icon action buttons */
  .actions { white-space: nowrap; }
  .icon-btn {
    background: none; border: none; cursor: pointer; padding: 4px;
    color: var(--text-muted); border-radius: 4px; line-height: 0;
  }
  .icon-btn:hover { color: var(--text); background: var(--hover-overlay); }
  .icon-btn:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
  .icon-btn.danger:hover { color: var(--danger); }
</style>
