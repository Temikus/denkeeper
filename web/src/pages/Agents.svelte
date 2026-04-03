<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let agents = $state([])
  let selected = $state(null)
  let detail = $state(null)
  let error = $state('')
  let expandedGroup = $state(null)

  // Persona editing state
  let expandedSection = $state(null)
  let sectionContent = $state('')
  let sectionEditable = $state(false)
  let sectionLoading = $state(false)
  let sectionSaving = $state(false)
  let sectionSaveOk = $state(false)

  onMount(async () => {
    try {
      agents = (await api.agents()) || []
      if (agents.length) selectAgent(agents[0])
    } catch(e) {
      error = e.message
    }
  })

  async function selectAgent(a) {
    if (!a) return
    selected = a
    detail = null
    expandedGroup = null
    expandedSection = null
    try {
      detail = await api.agent(a.name)
      initConfigForm(detail)
    } catch(e) {
      error = e.message
    }
  }

  async function toggleSection(sec, loaded) {
    if (!loaded) return
    if (expandedSection === sec) { expandedSection = null; return }
    sectionLoading = true
    sectionSaveOk = false
    try {
      const data = await api.getPersona(detail.name, sec)
      sectionContent = data.content
      sectionEditable = data.editable
      expandedSection = sec
    } catch(e) {
      error = e.message
    } finally {
      sectionLoading = false
    }
  }

  async function saveSection() {
    sectionSaving = true
    sectionSaveOk = false
    try {
      await api.updatePersona(detail.name, expandedSection, sectionContent)
      sectionSaveOk = true
      setTimeout(() => sectionSaveOk = false, 3000)
    } catch(e) {
      error = e.message
    } finally {
      sectionSaving = false
    }
  }

  function toolGroups(toolNames) {
    if (!toolNames || !toolNames.length) return {}
    const groups = {}
    for (const t of toolNames) {
      const idx = t.indexOf('_')
      const prefix = idx > 0 ? t.substring(0, idx) + '_*' : 'other'
      if (!groups[prefix]) groups[prefix] = []
      groups[prefix].push(t)
    }
    return groups
  }

  // Agent config editing state
  let configTier = $state('')
  let configModel = $state('')
  let configDescription = $state('')
  let configAllowlist = $state('')
  let configSaving = $state(false)
  let configSaveOk = $state(false)

  function initConfigForm(d) {
    configTier = d.permission_tier || 'supervised'
    configModel = d.model || ''
    configDescription = ''
    configAllowlist = ''
    // Try to get description and allowlist from the config
    if (agents.length) {
      const agentConf = agents.find(a => a.name === d.name)
      if (agentConf) {
        configDescription = agentConf.description || ''
        configAllowlist = (agentConf.browser_url_allowlist || []).join(', ')
      }
    }
  }

  async function saveConfig() {
    if (!detail) return
    configSaving = true
    configSaveOk = false
    try {
      const data = {}
      if (configTier !== detail.permission_tier) data.session_tier = configTier
      if (configModel !== detail.model) data.llm_model = configModel
      if (configDescription !== undefined) data.description = configDescription
      const allowlistArr = configAllowlist.split(',').map(s => s.trim()).filter(Boolean)
      data.browser_url_allowlist = allowlistArr
      await api.updateAgentConfig(detail.name, data)
      // Refresh detail.
      detail = await api.agent(detail.name)
      agents = (await api.agents()) || []
      configSaveOk = true
      setTimeout(() => configSaveOk = false, 3000)
    } catch(e) {
      error = e.message
    } finally {
      configSaving = false
    }
  }

  const defaultSections = ['soul', 'user', 'memory']

  function personaSections(d) {
    if (d.persona_sections) {
      return defaultSections.map(s => ({ name: s, loaded: !!d.persona_sections[s] }))
    }
    return defaultSections.map(s => ({ name: s, loaded: false }))
  }

  function tierLabel(tier) {
    if (!tier) return '—'
    return tier.charAt(0).toUpperCase() + tier.slice(1)
  }
</script>

<ErrorBanner message={error} />

<div class="layout">
  <aside class="list">
    {#each agents as a}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        class="item"
        class:active={selected?.name === a.name}
        onclick={() => selectAgent(a)}
        role="button"
        tabindex="0"
      >
        <div class="name">{a.name}</div>
        <div class="meta">{a.permission_tier} · {a.skill_count} skills</div>
      </div>
    {/each}
    {#if agents.length === 0 && !error}
      <p class="empty">No agents.</p>
    {/if}
  </aside>

  {#if detail}
    <section class="detail">
      <!-- Breadcrumb -->
      <div class="breadcrumb">
        <span class="bc-link">Agents</span>
        <span class="bc-sep">›</span>
        <span class="bc-current">{detail.name} Status</span>
      </div>

      <!-- Header -->
      <div class="agent-header">
        <div>
          <h1 class="agent-name">{detail.name}</h1>
          <p class="agent-subtitle">
            {detail.persona_dir ? 'Persona Active' : 'No Persona'}
            {#if detail.model}· {detail.model}{/if}
          </p>
        </div>
      </div>

      <!-- Stats cards -->
      <div class="stat-cards">
        <div class="stat-card">
          <div class="stat-icon" style="background: rgba(79,142,247,0.12); color: var(--accent);">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>
          </div>
          <div class="stat-text">
            <div class="stat-label">MODEL</div>
            <div class="stat-value mono">{detail.model || '—'}</div>
          </div>
        </div>
        <div class="stat-card">
          <div class="stat-icon" style="background: rgba(240,169,88,0.12); color: var(--warn);">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>
          </div>
          <div class="stat-text">
            <div class="stat-label">PERMISSION</div>
            <div class="stat-value">
              <span class="tier-badge tier-{detail.permission_tier}">{tierLabel(detail.permission_tier)}</span>
            </div>
          </div>
        </div>
        <div class="stat-card">
          <div class="stat-icon" style="background: rgba(76,175,125,0.12); color: var(--success);">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="7" width="20" height="14" rx="2"/><path d="M16 3h-8l-2 4h12z"/></svg>
          </div>
          <div class="stat-text">
            <div class="stat-label">HAS TOOLS</div>
            <div class="stat-value">
              {#if detail.tool_names && detail.tool_names.length > 0}
                <span class="tools-active">Active ({detail.tool_names.length})</span>
              {:else if detail.has_tools}
                <span class="tools-configured">Configured</span>
              {:else}
                <span class="tools-none">None</span>
              {/if}
            </div>
          </div>
        </div>
        <div class="stat-card">
          <div class="stat-icon" style="background: rgba(168,85,247,0.12); color: #a855f7;">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
          </div>
          <div class="stat-text">
            <div class="stat-label">ADAPTERS</div>
            <div class="stat-value">{(detail.adapters || []).join(', ') || '—'}</div>
          </div>
        </div>
      </div>

      <!-- Persona section (always shown) -->
      <div class="card">
        <div class="card-header">
          <div class="card-title">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M20.84 4.61a5.5 5.5 0 00-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 00-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 000-7.78z"/></svg>
            Persona
          </div>
          {#if detail.persona_dir}
            <span class="card-meta mono">{detail.persona_dir}</span>
          {/if}
        </div>

        <div class="persona-sections">
          {#each personaSections(detail) as { name: sec, loaded }}
            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_noninteractive_tabindex -->
            <div
              class="persona-section"
              class:loaded
              class:missing={!loaded}
              class:clickable={loaded}
              class:active={expandedSection === sec}
              onclick={() => toggleSection(sec, loaded)}
              role={loaded ? 'button' : undefined}
              tabindex={loaded ? 0 : undefined}
            >
              <div class="section-header">
                <span class="section-icon">
                  {#if sec === 'soul'}
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 9l9-7 9 7v11a2 2 0 01-2 2H5a2 2 0 01-2-2z"/></svg>
                  {:else if sec === 'user'}
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2"/><circle cx="12" cy="7" r="4"/></svg>
                  {:else}
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z"/><path d="M12 6v6l4 2"/></svg>
                  {/if}
                </span>
                <span class="section-name"># {sec.charAt(0).toUpperCase() + sec.slice(1)}</span>
                <span class="section-source">← {sec.toUpperCase()}.md</span>
              </div>
              {#if loaded}
                <div class="section-status loaded-status">{expandedSection === sec ? '\u25BC' : '\u25B6'} Loaded</div>
              {:else}
                <div class="section-status missing-status">Missing</div>
              {/if}
            </div>

            {#if expandedSection === sec}
              <div class="section-editor">
                {#if sectionLoading}
                  <p class="editor-loading">Loading…</p>
                {:else}
                  <textarea
                    class="editor-textarea"
                    bind:value={sectionContent}
                    readonly={!sectionEditable}
                    rows="12"
                  ></textarea>
                  <div class="editor-footer">
                    {#if !sectionEditable}
                      <span class="editor-hint">Read-only — this section cannot be edited via the dashboard.</span>
                    {:else}
                      <button class="btn-save" onclick={(e) => { e.stopPropagation(); saveSection() }} disabled={sectionSaving}>
                        {sectionSaving ? 'Saving…' : 'Save'}
                      </button>
                      {#if sectionSaveOk}
                        <span class="save-ok">Saved</span>
                      {/if}
                    {/if}
                  </div>
                {/if}
              </div>
            {/if}
          {/each}
        </div>
      </div>

      <!-- Agent Configuration -->
      <div class="card">
        <div class="card-header">
          <div class="card-title">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/></svg>
            Configuration
          </div>
        </div>
        <div class="config-form">
          <div class="config-row">
            <label class="config-label" for="cfg-tier">Permission Tier</label>
            <select id="cfg-tier" class="config-input" bind:value={configTier}>
              <option value="autonomous">Autonomous</option>
              <option value="supervised">Supervised</option>
              <option value="restricted">Restricted</option>
            </select>
          </div>
          <div class="config-row">
            <label class="config-label" for="cfg-model">LLM Model</label>
            <input id="cfg-model" class="config-input" type="text" bind:value={configModel} placeholder="e.g. anthropic/claude-sonnet-4-20250514" />
          </div>
          <div class="config-row">
            <label class="config-label" for="cfg-desc">Description</label>
            <input id="cfg-desc" class="config-input" type="text" bind:value={configDescription} placeholder="Agent description" />
          </div>
          <div class="config-row">
            <label class="config-label" for="cfg-allowlist">Browser URL Allowlist</label>
            <input id="cfg-allowlist" class="config-input" type="text" bind:value={configAllowlist} placeholder="e.g. *.example.com, api.service.io" />
            <span class="config-hint">Comma-separated domains. Empty = unrestricted.</span>
          </div>
          <div class="config-row">
            <label class="config-label">Adapters</label>
            <div class="config-readonly">{(detail.adapters || []).join(', ') || '—'} <span class="config-hint">(requires restart)</span></div>
          </div>
          <div class="config-actions">
            <button class="btn-save" onclick={saveConfig} disabled={configSaving}>
              {configSaving ? 'Saving…' : 'Save Config'}
            </button>
            {#if configSaveOk}
              <span class="save-ok">Saved</span>
            {/if}
          </div>
        </div>
      </div>

      <!-- Active Capabilities (Skills) -->
      <div class="card">
        <div class="card-header">
          <div class="card-title">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 19.5A2.5 2.5 0 016.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 014 19.5v-15A2.5 2.5 0 016.5 2z"/></svg>
            Active Capabilities
          </div>
        </div>
        {#if detail.skills && detail.skills.length > 0}
          <div class="capabilities-list">
            {#each detail.skills as sk}
              <div class="capability-item">
                <div class="capability-name">{sk.name}</div>
                {#if sk.description}
                  <div class="capability-desc">{sk.description}</div>
                {/if}
              </div>
            {/each}
          </div>
        {:else}
          <p class="empty">No skills loaded.</p>
        {/if}
      </div>

      <!-- Available Tools Inventory -->
      {#if detail.tool_names && detail.tool_names.length > 0}
        <div class="card">
          <div class="card-header">
            <div class="card-title">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14.7 6.3a1 1 0 000 1.4l1.6 1.6a1 1 0 001.4 0l3.77-3.77a6 6 0 01-7.94 7.94l-6.91 6.91a2.12 2.12 0 01-3-3l6.91-6.91a6 6 0 017.94-7.94l-3.76 3.76z"/></svg>
              Available Tools
            </div>
            <span class="tool-count">Total: {detail.tool_names.length}</span>
          </div>
          <div class="tool-pills">
            {#each Object.entries(toolGroups(detail.tool_names)) as [group, tools]}
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <span
                class="tool-pill"
                class:expanded={expandedGroup === group}
                onclick={() => expandedGroup = expandedGroup === group ? null : group}
                role="button"
                tabindex="0"
              >{group} <span class="pill-count">{tools.length}</span></span>
            {/each}
          </div>
          {#if expandedGroup && toolGroups(detail.tool_names)[expandedGroup]}
            <div class="tool-expand">
              {#each toolGroups(detail.tool_names)[expandedGroup] as t}
                <span class="tool-expand-item mono">{t}</span>
              {/each}
            </div>
          {/if}
        </div>
      {:else if detail.has_tools}
        <div class="card">
          <div class="card-header">
            <div class="card-title">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14.7 6.3a1 1 0 000 1.4l1.6 1.6a1 1 0 001.4 0l3.77-3.77a6 6 0 01-7.94 7.94l-6.91 6.91a2.12 2.12 0 01-3-3l6.91-6.91a6 6 0 017.94-7.94l-3.76 3.76z"/></svg>
              Available Tools
            </div>
          </div>
          <p class="empty">Configured — none registered yet.</p>
        </div>
      {/if}
    </section>
  {:else if !error && agents.length > 0}
    <p class="empty select-prompt">Select an agent to view details.</p>
  {/if}
</div>

<style>
  /* Layout */
  .layout { display: flex; gap: 20px; }
  .list { width: 200px; flex-shrink: 0; display: flex; flex-direction: column; gap: 6px; }
  .item {
    padding: 10px 12px;
    border-radius: var(--radius);
    cursor: pointer;
    border: 1px solid var(--border);
    background: var(--surface);
  }
  .item:hover, .item.active { border-color: var(--accent); }
  .name { font-weight: 600; }
  .meta { font-size: 12px; color: var(--text-muted); margin-top: 2px; }
  .detail { flex: 1; min-width: 0; }

  /* Breadcrumb */
  .breadcrumb { font-size: 13px; margin-bottom: 16px; }
  .bc-link { color: var(--text-muted); }
  .bc-sep { color: var(--text-muted); margin: 0 6px; }
  .bc-current { color: var(--text); font-weight: 500; }

  /* Header */
  .agent-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px; }
  .agent-name { font-size: 28px; font-weight: 700; margin: 0; line-height: 1.2; }
  .agent-subtitle { font-size: 14px; color: var(--text-muted); margin: 4px 0 0; }

  /* Stat cards */
  .stat-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin-bottom: 20px; }
  .stat-card {
    display: flex; align-items: center; gap: 12px;
    padding: 16px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .stat-icon {
    width: 40px; height: 40px; border-radius: 10px;
    display: flex; align-items: center; justify-content: center;
    flex-shrink: 0;
  }
  .stat-label { font-size: 11px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; font-weight: 500; }
  .stat-value { font-size: 13px; font-weight: 600; margin-top: 2px; }
  .mono { font-family: monospace; font-size: 12px; }

  /* Tier badge */
  .tier-badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 500;
  }
  .tier-autonomous { background: rgba(76,175,125,0.15); color: var(--success); }
  .tier-supervised { background: rgba(240,169,88,0.15); color: var(--warn); }
  .tier-restricted { background: rgba(224,92,110,0.15); color: var(--danger); }

  /* Tools status */
  .tools-active { color: var(--success); font-weight: 600; }
  .tools-configured { color: var(--warn); }
  .tools-none { color: var(--text-muted); }

  /* Cards */
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    margin-bottom: 20px;
  }
  .card-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 14px 18px;
    border-bottom: 1px solid var(--border);
  }
  .card-title {
    display: flex; align-items: center; gap: 8px;
    font-size: 14px; font-weight: 600;
  }
  .card-meta { font-size: 11px; color: var(--text-muted); }

  /* Persona sections */
  .persona-sections { padding: 16px 18px; display: flex; flex-direction: column; gap: 12px; }
  .persona-section {
    display: flex; align-items: center; justify-content: space-between;
    padding: 10px 14px;
    border-radius: var(--radius);
    background: rgba(255,255,255,0.02);
    border: 1px solid var(--border);
  }
  .persona-section.loaded { border-color: rgba(79,142,247,0.3); }
  .section-header { display: flex; align-items: center; gap: 8px; }
  .section-icon { display: flex; align-items: center; color: var(--text-muted); }
  .persona-section.loaded .section-icon { color: var(--accent); }
  .section-name { font-size: 13px; font-weight: 600; }
  .section-source { font-size: 11px; color: var(--text-muted); }
  .section-status { font-size: 12px; font-weight: 500; }
  .loaded-status { color: var(--success); }
  .missing-status { color: var(--danger); font-style: italic; }
  .persona-section.missing { border-style: dashed; opacity: 0.6; }
  .persona-section.missing:hover { opacity: 0.8; }
  .persona-section.clickable { cursor: pointer; }
  .persona-section.clickable:hover { border-color: var(--accent); }
  .persona-section.active { border-color: var(--accent); background: rgba(79,142,247,0.05); }

  /* Persona editor */
  .section-editor {
    padding: 12px 14px;
    background: rgba(255,255,255,0.01);
    border: 1px solid var(--border);
    border-top: none;
    border-radius: 0 0 var(--radius) var(--radius);
    margin-top: -1px;
  }
  .editor-loading { color: var(--text-muted); font-size: 13px; }
  .editor-textarea {
    width: 100%; background: var(--bg); border: 1px solid var(--border);
    border-radius: var(--radius); color: var(--text); padding: 10px 12px;
    font-family: monospace; font-size: 12px; resize: vertical; line-height: 1.5;
  }
  .editor-textarea:focus { outline: none; border-color: var(--accent); }
  .editor-textarea[readonly] { opacity: 0.7; cursor: default; }
  .editor-footer { display: flex; align-items: center; gap: 10px; margin-top: 8px; }
  .editor-hint { font-size: 11px; color: var(--text-muted); font-style: italic; }
  .btn-save {
    background: var(--accent); color: white; border: none;
    padding: 6px 16px; border-radius: var(--radius); cursor: pointer;
    font-size: 13px; font-weight: 500;
  }
  .btn-save:hover { background: var(--accent-hover); }
  .btn-save:disabled { opacity: 0.5; cursor: not-allowed; }
  .save-ok { font-size: 12px; color: var(--success); font-weight: 500; }

  /* Config form */
  .config-form { padding: 16px 18px; display: flex; flex-direction: column; gap: 14px; }
  .config-row { display: flex; flex-direction: column; gap: 4px; }
  .config-label { font-size: 12px; font-weight: 600; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.3px; }
  .config-input {
    background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
    color: var(--text); padding: 8px 12px; font-size: 13px; font-family: monospace;
  }
  .config-input:focus { outline: none; border-color: var(--accent); }
  select.config-input { cursor: pointer; }
  .config-hint { font-size: 11px; color: var(--text-muted); font-style: italic; }
  .config-readonly { font-size: 13px; font-family: monospace; color: var(--text); padding: 8px 0; }
  .config-actions { display: flex; align-items: center; gap: 10px; margin-top: 4px; }

  /* Capabilities */
  .capabilities-list { padding: 12px 18px; display: flex; flex-direction: column; gap: 8px; }
  .capability-item {
    padding: 10px 12px;
    border-radius: var(--radius);
    background: rgba(255,255,255,0.02);
    border: 1px solid var(--border);
  }
  .capability-name { font-size: 13px; font-weight: 600; }
  .capability-desc { font-size: 12px; color: var(--text-muted); margin-top: 2px; }

  /* Tools inventory */
  .tool-count {
    font-size: 11px; color: var(--text-muted);
    background: rgba(255,255,255,0.05);
    padding: 2px 8px;
    border-radius: 4px;
    border: 1px solid var(--border);
  }
  .tool-pills { padding: 14px 18px; display: flex; flex-wrap: wrap; gap: 8px; }
  .tool-pill {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 6px 12px;
    background: rgba(255,255,255,0.03);
    border: 1px solid var(--border);
    border-radius: 20px;
    font-size: 12px; font-family: monospace;
    color: var(--text);
    cursor: default;
  }
  .tool-pill:hover, .tool-pill.expanded { border-color: var(--accent); }
  .tool-pill.expanded { background: rgba(79,142,247,0.08); }
  .pill-count {
    background: rgba(79,142,247,0.15);
    color: var(--accent);
    font-size: 11px; font-weight: 600;
    padding: 1px 6px;
    border-radius: 10px;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  }

  /* Tool expand */
  .tool-expand {
    display: flex; flex-wrap: wrap; gap: 6px;
    padding: 0 18px 14px;
    border-top: 1px solid var(--border);
    padding-top: 12px;
  }
  .tool-expand-item {
    padding: 4px 10px;
    background: rgba(255,255,255,0.03);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    font-size: 12px;
    color: var(--text);
  }

  /* Misc */
  .empty { color: var(--text-muted); font-size: 13px; padding: 14px 18px; }
  .select-prompt { margin-top: 40px; text-align: center; }

  /* Responsive */
  @media (max-width: 900px) {
    .stat-cards { grid-template-columns: repeat(2, 1fr); }
  }
  @media (max-width: 600px) {
    .layout { flex-direction: column; }
    .list { width: 100%; flex-direction: row; overflow-x: auto; }
    .stat-cards { grid-template-columns: 1fr; }
  }
</style>
