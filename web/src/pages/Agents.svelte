<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import ModelSelector from '../components/ModelSelector.svelte'
  import FallbackRulesModal from '../components/FallbackRulesModal.svelte'

  let agents = $state([])
  let selected = $state(null)
  let detail = $state(null)
  let error = $state('')
  let expandedGroup = $state(null)
  let enabledProviders = $state([])  // ['anthropic', 'openrouter', ...]

  // Inline rename state
  let renamingAgent = $state(null)
  let renameValue = $state('')
  let renameSaving = $state(false)

  // Persona section state: keyed by section name
  let sectionData = $state({})   // { soul: { content, editable, agent_mutable }, ... }
  let expandedSection = $state(null)
  let sectionContent = $state('')
  let sectionEditable = $state(false)
  let sectionAgentMutable = $state(false)
  let sectionSaving = $state(false)
  let sectionSaveOk = $state(false)
  let sectionsLoading = $state(false)

  // Identity form fields (structured editing mode)
  let idName = $state('')
  let idEmoji = $state('')
  let idTheme = $state('')
  let idBody = $state('')
  let idRawMode = $state(false)

  onMount(async () => {
    try {
      const [agentList, providerData] = await Promise.all([
        api.agents(),
        api.llmProviders().catch(() => null),
      ])
      agents = agentList || []
      if (providerData?.providers) {
        enabledProviders = providerData.providers.filter(p => p.enabled).map(p => p.name)
      }
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
    expandedCard = null
    sectionData = {}
    try {
      detail = await api.agent(a.name)
      initConfigForm(detail)
      loadAllSections(a.name)
    } catch(e) {
      error = e.message
    }
  }

  function startRename(agentName, e) {
    e.stopPropagation()
    renamingAgent = agentName
    renameValue = agentName
  }

  async function confirmRename(oldName) {
    const newName = renameValue.trim()
    if (!newName || newName === oldName) { renamingAgent = null; return }
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(newName)) {
      error = 'Name must be lowercase alphanumeric with hyphens only'
      return
    }
    renameSaving = true
    try {
      await api.updateAgentConfig(oldName, { name: newName })
      agents = (await api.agents()) || []
      renamingAgent = null
      const updated = agents.find(a => a.name === newName)
      if (updated) selectAgent(updated)
    } catch (e) { error = e.message }
    finally { renameSaving = false }
  }

  function cancelRename() { renamingAgent = null }

  function autofocus(node) { node.focus(); node.select() }

  async function loadAllSections(agentName) {
    sectionsLoading = true
    const results = {}
    await Promise.all(defaultSections.map(async (sec) => {
      try {
        const data = await api.getPersona(agentName, sec)
        results[sec] = data
      } catch { /* section may not exist */ }
    }))
    sectionData = results
    sectionsLoading = false
  }

  // Parse identity content into structured fields for the form.
  function parseIdentityContent(content) {
    if (!content || !content.startsWith('---')) return { name: '', emoji: '', theme: '', body: content || '' }
    const end = content.indexOf('\n---', 3)
    if (end === -1) return { name: '', emoji: '', theme: '', body: content }
    const yaml = content.substring(3, end)
    const after = content.substring(end + 4).trim()
    const name = yaml.match(/^name:\s*(.+)$/m)?.[1]?.trim() || ''
    const emoji = yaml.match(/^emoji:\s*"?([^"\n]+)"?$/m)?.[1]?.trim() || ''
    const theme = yaml.match(/^theme:\s*(.+)$/m)?.[1]?.trim() || ''
    return { name, emoji, theme, body: after }
  }

  // Serialize identity form fields back to IDENTITY.md content.
  function serializeIdentity() {
    let fm = ''
    if (idName || idEmoji || idTheme) {
      const lines = ['---']
      if (idName) lines.push(`name: ${idName}`)
      if (idEmoji) lines.push(`emoji: "${idEmoji}"`)
      if (idTheme) lines.push(`theme: ${idTheme}`)
      lines.push('---')
      fm = lines.join('\n')
    }
    if (idBody) return fm ? fm + '\n\n' + idBody : idBody
    return fm
  }

  function toggleSection(sec) {
    if (expandedSection === sec) { expandedSection = null; return }
    sectionSaveOk = false
    const data = sectionData[sec]
    sectionContent = data?.content || ''
    sectionEditable = data?.editable ?? true
    sectionAgentMutable = data?.agent_mutable ?? false
    expandedSection = sec
    // Populate identity form fields when opening identity section.
    if (sec === 'identity') {
      const fields = parseIdentityContent(sectionContent)
      idName = fields.name
      idEmoji = fields.emoji
      idTheme = fields.theme
      idBody = fields.body
      idRawMode = false
    }
  }

  async function saveSection() {
    sectionSaving = true
    sectionSaveOk = false
    try {
      // For identity in form mode, serialize fields to content before saving.
      const contentToSave = (expandedSection === 'identity' && !idRawMode)
        ? serializeIdentity()
        : sectionContent
      await api.updatePersona(detail.name, expandedSection, contentToSave)
      // Update preview data
      sectionData[expandedSection] = { ...sectionData[expandedSection], content: contentToSave }
      sectionSaveOk = true
      expandedSection = null
      setTimeout(() => sectionSaveOk = false, 3000)
    } catch(e) {
      error = e.message
    } finally {
      sectionSaving = false
    }
  }

  function previewLines(sec) {
    const data = sectionData[sec]
    if (!data?.content) return ''
    // For identity, show parsed fields as a readable summary.
    if (sec === 'identity') {
      const fields = parseIdentityContent(data.content)
      const parts = []
      if (fields.name) parts.push(fields.emoji ? `${fields.emoji} ${fields.name}` : fields.name)
      if (fields.theme) parts.push(fields.theme)
      return parts.join(' — ') || data.content.split('\n').slice(0, 3).join('\n')
    }
    const lines = data.content.split('\n').slice(0, 3).join('\n')
    return lines.length < data.content.length ? lines + '...' : lines
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

  // Per-card expandable config state (accordion — one at a time)
  let expandedCard = $state(null)  // 'model' | 'permission' | 'tools' | 'adapters' | null
  let configTier = $state('')
  let configModel = $state('')
  let configProvider = $state('')
  let configDescription = $state('')
  let configAllowlist = $state('')
  let configSaving = $state(false)
  let configSaveOk = $state(false)

  // Fallback rules modal
  let showFallbackModal = $state(false)
  let fallbackRules = $state([])

  function initConfigForm(d) {
    configTier = d.permission_tier || 'supervised'
    configModel = d.model || ''
    configProvider = d.provider || ''
    configDescription = ''
    configAllowlist = ''
    if (agents.length) {
      const agentConf = agents.find(a => a.name === d.name)
      if (agentConf) {
        configDescription = agentConf.description || ''
        configAllowlist = (agentConf.browser_url_allowlist || []).join(', ')
      }
    }
  }

  function toggleCard(card) {
    if (expandedCard === card) { expandedCard = null; return }
    initConfigForm(detail)
    configSaveOk = false
    expandedCard = card
  }

  function cancelCard() {
    expandedCard = null
    configSaveOk = false
  }

  async function saveCardConfig() {
    if (!detail) return
    configSaving = true
    configSaveOk = false
    try {
      const data = {}
      if (expandedCard === 'model') {
        if (configModel !== detail.model) data.llm_model = configModel
        if (configProvider && configProvider !== detail.provider) data.llm_provider = configProvider
        if (configDescription !== undefined) data.description = configDescription
      } else if (expandedCard === 'permission') {
        if (configTier !== detail.permission_tier) data.session_tier = configTier
      }
      if (Object.keys(data).length) {
        await api.updateAgentConfig(detail.name, data)
        detail = await api.agent(detail.name)
        agents = (await api.agents()) || []
        initConfigForm(detail)
      }
      configSaveOk = true
      expandedCard = null
      setTimeout(() => configSaveOk = false, 3000)
    } catch(e) {
      error = e.message
    } finally {
      configSaving = false
    }
  }

  function openFallbackModal() {
    fallbackRules = JSON.parse(JSON.stringify(detail.fallbacks || []))
    showFallbackModal = true
  }

  async function saveFallbackRules(rules) {
    error = ''
    try {
      await api.updateAgentConfig(detail.name, { fallbacks: rules })
      detail = await api.agent(detail.name)
      showFallbackModal = false
    } catch(e) {
      error = e.message
    }
  }

  // Allowlist editing (in Available Tools section)
  let allowlistSaving = $state(false)
  let allowlistSaveOk = $state(false)

  function isBrowserGroup(group) {
    return group.startsWith('browser_') || group.startsWith('web_')
  }

  async function saveAllowlist() {
    if (!detail) return
    allowlistSaving = true
    allowlistSaveOk = false
    try {
      const arr = configAllowlist.split(',').map(s => s.trim()).filter(Boolean)
      await api.updateAgentConfig(detail.name, { browser_url_allowlist: arr })
      detail = await api.agent(detail.name)
      agents = (await api.agents()) || []
      initConfigForm(detail)
      allowlistSaveOk = true
      setTimeout(() => allowlistSaveOk = false, 3000)
    } catch(e) {
      error = e.message
    } finally {
      allowlistSaving = false
    }
  }

  const defaultSections = ['identity', 'soul', 'user', 'memory']

  // Parse identity frontmatter fields from raw content for display in headers.
  function parseIdentityFields(content) {
    if (!content || !content.startsWith('---')) return null
    const end = content.indexOf('\n---', 3)
    if (end === -1) return null
    const yaml = content.substring(3, end)
    const name = yaml.match(/^name:\s*(.+)$/m)?.[1]?.trim()
    const emoji = yaml.match(/^emoji:\s*"?([^"\n]+)"?$/m)?.[1]?.trim()
    return (name || emoji) ? { name, emoji } : null
  }

  // Returns display name enriched with identity data when available.
  function agentDisplayName(agentName) {
    const fields = parseIdentityFields(sectionData.identity?.content)
    if (!fields) return agentName
    let display = fields.name || agentName
    if (fields.emoji) display = fields.emoji + ' ' + display
    return display
  }

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
        onclick={() => { if (renamingAgent !== a.name) selectAgent(a) }}
        role="button"
        tabindex="0"
      >
        {#if renamingAgent === a.name}
          <div class="name-edit">
            <div class="name-edit-row">
              <!-- svelte-ignore a11y_autofocus -->
              <input
                class="name-input"
                bind:value={renameValue}
                use:autofocus
                disabled={renameSaving}
                onkeydown={(e) => { if (e.key === 'Enter') confirmRename(a.name); if (e.key === 'Escape') cancelRename() }}
              />
              <button class="name-action-btn" onclick={(e) => { e.stopPropagation(); confirmRename(a.name) }} disabled={renameSaving} title="Confirm">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>
              </button>
              <button class="name-action-btn" onclick={(e) => { e.stopPropagation(); cancelRename() }} title="Cancel">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
              </button>
            </div>
            <div class="rename-hint">Enter to save · Esc to cancel</div>
          </div>
        {:else}
          <div class="name">
            <span class="name-text">{a.display_name || a.name}</span>
            {#if a.name !== 'default'}
              <button class="edit-btn" onclick={(e) => startRename(a.name, e)} title="Rename agent">
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 3a2.83 2.83 0 114 4L7.5 20.5 2 22l1.5-5.5L17 3z"/></svg>
              </button>
            {/if}
          </div>
        {/if}
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
          <h1 class="agent-name">{agentDisplayName(detail.name)}</h1>
          <p class="agent-subtitle">
            {detail.persona_dir ? 'Persona Active' : 'No Persona'}
            {#if detail.model}· {detail.model}{/if}
          </p>
        </div>
        {#if configSaveOk}
          <span class="save-ok">Saved</span>
        {/if}
      </div>

      <!-- Stats cards — summary row -->
      <div class="stat-cards">
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div class="stat-card" class:expanded={expandedCard === 'model'} onclick={() => toggleCard('model')} role="button" tabindex="0" aria-expanded={expandedCard === 'model'}>
          <div class="stat-icon" style="background: rgba(200, 78, 53, 0.12); color: var(--accent);">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>
          </div>
          <div class="stat-text">
            <div class="stat-label">MODEL</div>
            <div class="stat-value mono">{detail.provider ? detail.provider + ' / ' : ''}{detail.model || '—'}</div>
          </div>
          <svg class="chevron-toggle down" class:open={expandedCard === 'model'} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="6 9 12 15 18 9"/></svg>
        </div>
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div class="stat-card" class:expanded={expandedCard === 'permission'} onclick={() => toggleCard('permission')} role="button" tabindex="0" aria-expanded={expandedCard === 'permission'}>
          <div class="stat-icon" style="background: rgba(240,169,88,0.12); color: var(--warn);">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>
          </div>
          <div class="stat-text">
            <div class="stat-label">PERMISSION</div>
            <div class="stat-value">
              <span class="tier-badge tier-{detail.permission_tier}">{tierLabel(detail.permission_tier)}</span>
            </div>
          </div>
          <svg class="chevron-toggle down" class:open={expandedCard === 'permission'} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="6 9 12 15 18 9"/></svg>
        </div>
        <div class="stat-card stat-card-static">
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
        <div class="stat-card stat-card-static">
          <div class="stat-icon" style="background: rgba(168,85,247,0.12); color: #a855f7;">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/><polyline points="22,6 12,13 2,6"/></svg>
          </div>
          <div class="stat-text">
            <div class="stat-label">ADAPTERS</div>
            <div class="stat-value">{(detail.adapters || []).join(', ') || '—'}</div>
          </div>
        </div>
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div class="stat-card" onclick={openFallbackModal} role="button" tabindex="0">
          <div class="stat-icon" style="background: rgba(220,120,60,0.12); color: #dc783c;">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>
          </div>
          <div class="stat-text">
            <div class="stat-label">FALLBACKS</div>
            <div class="stat-value">{(detail.fallbacks || []).length} rule{(detail.fallbacks || []).length !== 1 ? 's' : ''}</div>
          </div>
          <svg class="chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>
        </div>
      </div>

      <!-- Full-width config panel below cards -->
      {#if expandedCard}
        <div class="config-panel" aria-hidden={!expandedCard}>
          {#if expandedCard === 'model'}
            <div class="config-panel-title">Model Configuration</div>
            <div class="config-panel-body">
              <label class="config-label" for="cfg-provider">Provider</label>
              <select id="cfg-provider" class="config-input" bind:value={configProvider}>
                {#if configProvider && !enabledProviders.includes(configProvider)}
                  <option value={configProvider}>{configProvider}</option>
                {/if}
                {#each enabledProviders as p}
                  <option value={p}>{p}</option>
                {/each}
              </select>
              <label class="config-label" for="cfg-model">Model</label>
              <ModelSelector bind:value={configModel} onchange={(id, prov) => { if (prov) configProvider = prov }} />
              <label class="config-label" for="cfg-desc">Description</label>
              <input id="cfg-desc" class="config-input" type="text" bind:value={configDescription} placeholder="Agent description" />
            </div>
          {:else if expandedCard === 'permission'}
            <div class="config-panel-title">Permission Configuration</div>
            <div class="config-panel-body">
              <label class="config-label" for="cfg-tier">Permission Tier</label>
              <select id="cfg-tier" class="config-input" bind:value={configTier}>
                <option value="autonomous">Autonomous</option>
                <option value="supervised">Supervised</option>
                <option value="restricted">Restricted</option>
              </select>
            </div>
          {/if}
          <div class="config-panel-actions">
            <button class="btn-save" onclick={saveCardConfig} disabled={configSaving}>
              {configSaving ? 'Saving…' : 'Save'}
            </button>
            <button class="btn-ghost btn-ghost-sm" onclick={cancelCard}>Cancel</button>
          </div>
        </div>
      {/if}

      <!-- Persona -->
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
            <div class="sp-section" class:active={expandedSection === sec}>
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <div class="sp-header" onclick={() => toggleSection(sec)} role="button" tabindex="0" aria-expanded={expandedSection === sec}>
                <div class="sp-label">
                  <span class="section-icon">
                    {#if sec === 'identity'}
                      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="16" rx="2"/><line x1="7" y1="9" x2="17" y2="9"/><line x1="7" y1="13" x2="13" y2="13"/></svg>
                    {:else if sec === 'soul'}
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
                <div class="sp-badges">
                  {#if sectionData[sec]}
                    <span class="agent-badge" class:writable={sectionData[sec].agent_mutable}>
                      {sectionData[sec].agent_mutable ? 'agent: rw' : 'agent: ro'}
                    </span>
                  {/if}
                </div>
              </div>

              {#if expandedSection !== sec}
                {#if sectionsLoading}
                  <div class="sp-preview muted">Loading…</div>
                {:else if previewLines(sec)}
                  <!-- svelte-ignore a11y_click_events_have_key_events -->
                  <div class="sp-preview" onclick={() => toggleSection(sec)} role="button" tabindex="0">{previewLines(sec)}</div>
                {:else}
                  <!-- svelte-ignore a11y_click_events_have_key_events -->
                  <div class="sp-preview sp-empty" onclick={() => toggleSection(sec)} role="button" tabindex="0">Empty — click to add content</div>
                {/if}
              {:else}
                <div class="section-editor">
                  {#if sec === 'identity' && !idRawMode}
                    <!-- Identity structured form -->
                    <div class="identity-form">
                      <div class="id-field-row">
                        <div class="id-field">
                          <label class="config-label" for="id-name">Name</label>
                          <input id="id-name" class="config-input" type="text" bind:value={idName} placeholder="Agent name" readonly={!sectionEditable} />
                        </div>
                        <div class="id-field id-field-sm">
                          <label class="config-label" for="id-emoji">Emoji</label>
                          <input id="id-emoji" class="config-input" type="text" bind:value={idEmoji} placeholder="e.g. 🤖" readonly={!sectionEditable} />
                        </div>
                      </div>
                      <label class="config-label" for="id-theme">Theme / Vibe</label>
                      <input id="id-theme" class="config-input" type="text" bind:value={idTheme} placeholder="e.g. thorough and methodical" readonly={!sectionEditable} />
                      <label class="config-label" for="id-body">Notes <span class="text-muted">(optional)</span></label>
                      <textarea id="id-body" class="editor-textarea" bind:value={idBody} readonly={!sectionEditable} rows="4" placeholder="Additional identity notes..."></textarea>
                    </div>
                  {:else}
                    <textarea
                      class="editor-textarea"
                      bind:value={sectionContent}
                      readonly={!sectionEditable}
                      rows="12"
                    ></textarea>
                  {/if}
                  <div class="editor-footer">
                    {#if sec === 'identity'}
                      <button class="btn-ghost-sm" onclick={(e) => {
                        e.stopPropagation()
                        if (!idRawMode) {
                          // Switching to raw: serialize fields into sectionContent
                          sectionContent = serializeIdentity()
                        } else {
                          // Switching to form: parse raw content back to fields
                          const fields = parseIdentityContent(sectionContent)
                          idName = fields.name; idEmoji = fields.emoji
                          idTheme = fields.theme; idBody = fields.body
                        }
                        idRawMode = !idRawMode
                      }}>{idRawMode ? 'Form' : 'Raw'}</button>
                    {/if}
                    <button class="btn-save" onclick={(e) => { e.stopPropagation(); saveSection() }} disabled={sectionSaving}>
                      {sectionSaving ? 'Saving…' : 'Save'}
                    </button>
                    {#if sectionSaveOk}
                      <span class="save-ok">Saved</span>
                    {/if}
                    <span class="agent-mutable-hint">
                      {#if sectionAgentMutable}
                        <span class="dot-agent-rw"></span> Agent can write
                      {:else}
                        <span class="dot-agent-ro"></span> Agent read-only
                      {/if}
                    </span>
                  </div>
                </div>
              {/if}
            </div>
          {/each}
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
            {#if isBrowserGroup(expandedGroup)}
              <div class="tool-allowlist">
                <label class="config-label" for="cfg-allowlist">Browser URL Allowlist</label>
                <div class="tool-allowlist-row">
                  <input id="cfg-allowlist" class="config-input" type="text" bind:value={configAllowlist} placeholder="e.g. *.example.com, api.service.io" />
                  <button class="btn-save btn-save-sm" onclick={saveAllowlist} disabled={allowlistSaving}>
                    {allowlistSaving ? 'Saving…' : 'Save'}
                  </button>
                </div>
                <span class="hint">Comma-separated domains. Empty = unrestricted.{#if allowlistSaveOk} <span class="save-ok-inline">Saved</span>{/if}</span>
              </div>
            {/if}
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

{#if showFallbackModal}
  <FallbackRulesModal
    bind:rules={fallbackRules}
    onSave={saveFallbackRules}
    onClose={() => { showFallbackModal = false }}
  />
{/if}

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
  .name { font-weight: 600; display: flex; align-items: center; gap: 4px; }
  .name-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .edit-btn {
    opacity: 0; transition: opacity 0.15s; background: none; border: none;
    cursor: pointer; padding: 2px; color: var(--text-muted); flex-shrink: 0;
    display: flex; align-items: center;
  }
  .edit-btn:hover { color: var(--text); }
  .item:hover .edit-btn { opacity: 1; }
  .name-edit { display: flex; flex-direction: column; gap: 2px; }
  .name-edit-row { display: flex; align-items: center; gap: 2px; }
  .name-input {
    font-weight: 600; font-size: inherit; font-family: inherit;
    background: var(--bg); border: 1px solid var(--accent); border-radius: 3px;
    padding: 1px 4px; width: 100%; outline: none; color: var(--text);
  }
  .name-action-btn {
    background: none; border: none; cursor: pointer; padding: 2px;
    color: var(--text-muted); display: flex; align-items: center; flex-shrink: 0;
  }
  .name-action-btn:hover { color: var(--text); }
  .rename-hint { font-size: 10px; color: var(--text-muted); }
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
  .stat-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin-bottom: 12px; }
  .stat-card {
    display: flex; align-items: center; gap: 12px;
    padding: 16px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    cursor: pointer;
    transition: border-color 0.15s;
  }
  .stat-card:hover { border-color: var(--accent); }
  .stat-card.expanded { border-color: var(--accent); }
  .stat-card-static { cursor: default; }
  .stat-card-static:hover { border-color: var(--border); }
  .stat-icon {
    width: 40px; height: 40px; border-radius: 10px;
    display: flex; align-items: center; justify-content: center;
    flex-shrink: 0;
  }
  .stat-text { flex: 1; min-width: 0; }
  .stat-label { font-size: 11px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; font-weight: 500; }
  .stat-value { font-size: 13px; font-weight: 600; margin-top: 2px; }
  /* Chevron rotation: uses shared .chevron-toggle from shared.css */

  /* Full-width config panel below stat cards */
  .config-panel {
    background: var(--surface);
    border: 1px solid var(--accent);
    border-radius: var(--radius);
    padding: 16px 20px;
    margin-bottom: 20px;
  }
  .config-panel-title { font-size: 13px; font-weight: 600; margin-bottom: 12px; }
  .config-panel-body { display: flex; flex-direction: column; gap: 8px; }
  .config-label { font-size: 11px; font-weight: 500; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.3px; }
  .config-input {
    background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
    color: var(--text); padding: 6px 10px; font-size: 12px; width: 100%;
  }
  .config-input:focus { outline: none; border-color: var(--accent); }
  select.config-input { cursor: pointer; }
  .config-readonly { display: flex; flex-direction: column; gap: 4px; margin-bottom: 4px; }
  .config-panel-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 12px; }
  .btn-ghost-sm { padding: 5px 12px; font-size: 12px; }

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

  /* System Prompt Assembly sections */
  .persona-sections { padding: 16px 18px; display: flex; flex-direction: column; gap: 20px; }
  .sp-section { }
  .sp-header {
    display: flex; align-items: center; justify-content: space-between;
    cursor: pointer; padding: 2px 0; margin-bottom: 8px;
  }
  .sp-header:hover .section-name { color: var(--accent); }
  .sp-label { display: flex; align-items: center; gap: 8px; }
  .section-icon { display: flex; align-items: center; color: var(--text-muted); }
  .sp-section.active .section-icon { color: var(--accent); }
  .section-name { font-size: 13px; font-weight: 600; transition: color 0.1s; }
  .section-source { font-size: 11px; color: var(--text-muted); }
  .sp-badges { display: flex; gap: 6px; }
  .agent-badge {
    font-size: 10px; padding: 2px 7px; border-radius: 4px;
    font-family: monospace; letter-spacing: 0.3px;
    background: var(--hover-overlay); border: 1px solid var(--border); color: var(--text-muted);
  }
  .agent-badge.writable { border-color: rgba(76,175,125,0.3); color: var(--success); }

  /* Content preview block */
  .sp-preview {
    background: var(--hover-overlay);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 12px 14px;
    font-family: monospace; font-size: 12px; line-height: 1.6;
    color: var(--text-muted);
    white-space: pre-wrap; word-break: break-word;
    cursor: pointer;
    transition: border-color 0.15s;
  }
  .sp-preview:hover { border-color: var(--accent); }
  .sp-empty { font-style: italic; border-style: dashed; opacity: 0.6; }
  .sp-empty:hover { opacity: 1; }

  /* Persona editor (expanded) */
  .section-editor { margin-top: 8px; }
  .editor-textarea {
    width: 100%; background: var(--bg); border: 1px solid var(--border);
    border-radius: var(--radius); color: var(--text); padding: 10px 12px;
    font-family: monospace; font-size: 12px; resize: vertical; line-height: 1.5;
  }
  .editor-textarea:focus { outline: none; border-color: var(--accent); }
  .editor-textarea[readonly] { opacity: 0.7; cursor: default; }
  .editor-footer { display: flex; align-items: center; gap: 10px; margin-top: 8px; }

  /* Identity form */
  .identity-form { display: flex; flex-direction: column; gap: 8px; }
  .id-field-row { display: flex; gap: 12px; }
  .id-field { flex: 1; display: flex; flex-direction: column; gap: 4px; }
  .id-field-sm { flex: 0 0 80px; }
  .identity-form .editor-textarea { font-family: inherit; }
  .text-muted { color: var(--text-muted); font-weight: 400; }
  .agent-mutable-hint { margin-left: auto; font-size: 11px; color: var(--text-muted); display: flex; align-items: center; gap: 5px; }
  .dot-agent-rw { display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: var(--success); }
  .dot-agent-ro { display: inline-block; width: 6px; height: 6px; border-radius: 50%; background: var(--text-muted); }

  /* Capabilities */
  .capabilities-list { padding: 12px 18px; display: flex; flex-direction: column; gap: 8px; }
  .capability-item {
    padding: 10px 12px;
    border-radius: var(--radius);
    background: var(--hover-overlay);
    border: 1px solid var(--border);
  }
  .capability-name { font-size: 13px; font-weight: 600; }
  .capability-desc { font-size: 12px; color: var(--text-muted); margin-top: 2px; }

  /* Tools inventory */
  .tool-count {
    font-size: 11px; color: var(--text-muted);
    background: var(--hover-overlay);
    padding: 2px 8px;
    border-radius: 4px;
    border: 1px solid var(--border);
  }
  .tool-pills { padding: 14px 18px; display: flex; flex-wrap: wrap; gap: 8px; }
  .tool-pill {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 6px 12px;
    background: var(--hover-overlay);
    border: 1px solid var(--border);
    border-radius: 20px;
    font-size: 12px; font-family: monospace;
    color: var(--text);
    cursor: default;
  }
  .tool-pill:hover, .tool-pill.expanded { border-color: var(--accent); }
  .tool-pill.expanded { background: rgba(200, 78, 53, 0.08); }
  .pill-count {
    background: rgba(200, 78, 53, 0.15);
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
    background: var(--hover-overlay);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    font-size: 12px;
    color: var(--text);
  }

  /* Tool allowlist config */
  .tool-allowlist {
    padding: 0 18px 14px;
    border-top: 1px solid var(--border);
    padding-top: 12px;
    display: flex; flex-direction: column; gap: 6px;
  }
  .tool-allowlist-row { display: flex; gap: 8px; align-items: center; }
  .tool-allowlist-row .config-input { flex: 1; }
  .btn-save-sm { padding: 6px 14px; font-size: 12px; flex-shrink: 0; }
  .save-ok-inline { color: var(--success); font-weight: 500; }

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
  @media (prefers-reduced-motion: reduce) {
    .stat-card, .sp-preview, .section-name, .config-panel { transition: none; }
  }
</style>
