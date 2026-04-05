<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'

  let tools = []
  let plugins = []
  let loading = true
  let error = ''

  // Tool inline form (shared for add/edit)
  let showToolForm = false
  let editingToolName = null // non-null means edit mode
  let toolName = ''
  let toolTransport = 'stdio'
  let toolCommand = ''
  let toolArgs = ''
  let toolURL = ''
  let toolHeaderPairs = []
  let toolTimeoutSecs = ''
  let toolEnvPairs = []
  let addingTool = false
  let loadingToolConfig = false

  // Add plugin inline form
  let showPluginForm = false
  let pluginName = ''
  let pluginType = 'subprocess'
  let pluginCommand = ''
  let pluginImage = ''
  let pluginArgs = ''
  let pluginEnvPairs = []
  let pluginCapabilities = ['tools']
  let pluginMemoryLimit = ''
  let pluginCPULimit = ''
  let pluginNetwork = ''
  let addingPlugin = false

  // Restart tool
  let restartingTool = null

  // Tool defs modal
  let defsModal = null // { serverName, totalCount }
  let defsLoading = false
  let defsTools = [] // { name, description, parameters }
  let defsFilter = ''
  let expandedTool = null // tool name currently expanded, single-expand

  // Category inference from tool name
  const categoryKeywords = {
    'File operations': ['file', 'folder', 'directory', 'upload', 'download', 'read_file', 'write_file', 'create_file', 'delete_file', 'move_file', 'copy_file', 'list_files', 'get_file'],
    'Search': ['search', 'find', 'query', 'lookup', 'filter'],
    'Communication': ['send', 'message', 'notify', 'email', 'chat', 'post_message', 'slack', 'channel'],
    'Data': ['get', 'fetch', 'list', 'read', 'retrieve', 'export', 'import'],
    'Modification': ['create', 'update', 'delete', 'remove', 'add', 'set', 'edit', 'modify', 'write', 'put', 'patch'],
    'Scheduling': ['schedule', 'calendar', 'event', 'reminder', 'task', 'todo'],
    'Authentication': ['auth', 'login', 'token', 'session', 'oauth'],
  }

  function inferCategory(toolName) {
    const lower = toolName.toLowerCase()
    for (const [category, keywords] of Object.entries(categoryKeywords)) {
      for (const kw of keywords) {
        if (lower.includes(kw)) return category
      }
    }
    return 'Other'
  }

  function inferAccessTag(toolDef) {
    const name = toolDef.name.toLowerCase()
    const desc = (toolDef.description || '').toLowerCase()
    const combined = name + ' ' + desc
    const writeKeywords = ['create', 'update', 'delete', 'remove', 'write', 'send', 'post', 'put', 'patch', 'modify', 'add', 'set', 'move', 'copy', 'upload', 'execute', 'run']
    for (const kw of writeKeywords) {
      if (combined.includes(kw)) return 'write'
    }
    return 'read'
  }

  function groupByCategory(tools) {
    const groups = {}
    for (const t of tools) {
      const cat = inferCategory(t.name)
      if (!groups[cat]) groups[cat] = []
      groups[cat].push(t)
    }
    // Sort categories: named categories first, "Other" last
    const sorted = Object.entries(groups).sort(([a], [b]) => {
      if (a === 'Other') return 1
      if (b === 'Other') return -1
      return a.localeCompare(b)
    })
    return sorted
  }

  $: filteredDefs = defsFilter.trim()
    ? defsTools.filter(t => {
        const q = defsFilter.toLowerCase()
        return t.name.toLowerCase().includes(q) || (t.description || '').toLowerCase().includes(q)
      })
    : defsTools
  $: groupedDefs = groupByCategory(filteredDefs)

  async function openDefsModal(serverName, totalCount) {
    defsModal = { serverName, totalCount }
    defsFilter = ''
    expandedTool = null
    defsLoading = true
    defsTools = []
    try {
      const res = await api.toolDefs(serverName)
      defsTools = res.tools || []
    } catch (e) {
      error = e.message
      defsModal = null
    } finally {
      defsLoading = false
    }
  }

  function extractParams(td) {
    const params = td.parameters
    if (!params || !params.properties) return []
    const required = new Set(params.required || [])
    return Object.entries(params.properties).map(([name, schema]) => ({
      name,
      type: schema.enum ? 'enum' : (schema.type || 'any'),
      description: schema.description || '',
      required: required.has(name),
    }))
  }

  function toggleTool(name) {
    expandedTool = expandedTool === name ? null : name
  }

  function categoryIcon(category) {
    switch (category) {
      case 'File operations': return 'file'
      case 'Search': return 'search'
      case 'Communication': return 'comm'
      case 'Modification': return 'modify'
      case 'Scheduling': return 'schedule'
      case 'Authentication': return 'auth'
      default: return 'default'
    }
  }

  // Confirm remove
  let confirmRemove = null // { kind: 'tool'|'plugin', name }
  let removing = false

  async function loadData() {
    loading = true
    error = ''
    try {
      const [toolsRes, pluginsRes] = await Promise.all([
        api.listTools().catch(() => ({ tools: [] })),
        api.listPlugins().catch(() => ({ plugins: [] })),
      ])
      tools = toolsRes.tools || []
      plugins = pluginsRes.plugins || []
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function parseArgs(str) {
    return str.trim() ? str.trim().split(/\s+/) : []
  }

  function envPairsToObj(pairs) {
    const obj = {}
    for (const p of pairs) {
      if (p.key.trim()) obj[p.key.trim()] = p.value
    }
    return obj
  }

  function objToEnvPairs(obj) {
    if (!obj || Object.keys(obj).length === 0) return []
    return Object.entries(obj).map(([key, value]) => ({ key, value }))
  }

  function resetToolForm() {
    editingToolName = null
    toolName = ''; toolTransport = 'stdio'; toolCommand = ''; toolArgs = ''
    toolURL = ''; toolHeaderPairs = []; toolTimeoutSecs = ''; toolEnvPairs = []
  }

  function openAddToolForm() {
    resetToolForm()
    showToolForm = true
  }

  async function openEditToolForm(name) {
    if (editingToolName === name) {
      resetToolForm()
      return
    }
    resetToolForm()
    editingToolName = name
    toolName = name
    loadingToolConfig = true
    showToolForm = false // edit form is inline in the card, not the top panel
    error = ''
    try {
      const info = await api.getTool(name)
      toolTransport = info.transport || 'stdio'
      toolCommand = info.command || ''
      toolArgs = (info.args || []).join(' ')
      toolURL = info.url || ''
      toolHeaderPairs = objToEnvPairs(info.headers)
      toolTimeoutSecs = info.request_timeout_secs ? String(info.request_timeout_secs) : ''
      toolEnvPairs = objToEnvPairs(info.env)
    } catch (e) {
      error = e.message
      showToolForm = false
      editingToolName = null
    } finally {
      loadingToolConfig = false
    }
  }

  function toolFormValid() {
    if (!toolName.trim()) return false
    if (toolTransport === 'stdio' && !toolCommand.trim()) return false
    if (toolTransport === 'sse' && !toolURL.trim()) return false
    return true
  }

  function buildToolConfig() {
    const cfg = {}
    if (toolTransport === 'sse') {
      cfg.transport = 'sse'
      cfg.url = toolURL.trim()
      const headers = envPairsToObj(toolHeaderPairs)
      if (Object.keys(headers).length > 0) cfg.headers = headers
    } else {
      cfg.command = toolCommand.trim()
      const args = parseArgs(toolArgs)
      if (args.length > 0) cfg.args = args
    }
    const env = envPairsToObj(toolEnvPairs)
    if (Object.keys(env).length > 0) cfg.env = env
    const timeout = parseInt(toolTimeoutSecs, 10)
    if (timeout > 0) cfg.request_timeout_secs = timeout
    return cfg
  }

  async function submitToolForm() {
    if (!toolFormValid()) return
    addingTool = true
    error = ''
    try {
      const cfg = buildToolConfig()
      if (editingToolName) {
        await api.updateTool(editingToolName, cfg)
      } else {
        cfg.name = toolName.trim()
        await api.addTool(cfg)
      }
      showToolForm = false
      resetToolForm()
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      addingTool = false
    }
  }

  async function addPlugin() {
    if (!pluginName.trim()) return
    addingPlugin = true
    error = ''
    try {
      const cfg = {
        name: pluginName.trim(),
        type: pluginType,
        command: pluginCommand.trim() || undefined,
        image: pluginImage.trim() || undefined,
        args: parseArgs(pluginArgs),
        env: envPairsToObj(pluginEnvPairs),
        capabilities: pluginCapabilities,
        memory_limit: pluginMemoryLimit || undefined,
        cpu_limit: pluginCPULimit || undefined,
        network: pluginNetwork || undefined,
      }
      await api.addPlugin(cfg)
      showPluginForm = false
      pluginName = ''; pluginCommand = ''; pluginImage = ''; pluginArgs = ''
      pluginEnvPairs = []; pluginCapabilities = ['tools']
      pluginMemoryLimit = ''; pluginCPULimit = ''; pluginNetwork = ''
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      addingPlugin = false
    }
  }

  async function doRemove() {
    if (!confirmRemove) return
    removing = true
    error = ''
    try {
      if (confirmRemove.kind === 'tool') {
        await api.removeTool(confirmRemove.name)
      } else {
        await api.removePlugin(confirmRemove.name)
      }
      confirmRemove = null
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      removing = false
    }
  }

  function addKVPair(target) {
    if (target === 'tool-env') {
      toolEnvPairs = [...toolEnvPairs, { key: '', value: '' }]
    } else if (target === 'tool-headers') {
      toolHeaderPairs = [...toolHeaderPairs, { key: '', value: '' }]
    } else if (target === 'plugin') {
      pluginEnvPairs = [...pluginEnvPairs, { key: '', value: '' }]
    }
  }

  function removeKVPair(target, idx) {
    if (target === 'tool-env') {
      toolEnvPairs = toolEnvPairs.filter((_, i) => i !== idx)
    } else if (target === 'tool-headers') {
      toolHeaderPairs = toolHeaderPairs.filter((_, i) => i !== idx)
    } else if (target === 'plugin') {
      pluginEnvPairs = pluginEnvPairs.filter((_, i) => i !== idx)
    }
  }

  async function restartTool(name) {
    restartingTool = name
    error = ''
    try {
      await api.restartTool(name)
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      restartingTool = null
    }
  }

  function statusDot(status) {
    if (status === 'connected') return 'green'
    if (status === 'error') return 'red'
    if (status === 'disabled') return 'red'
    return 'grey'
  }

  function statusLabel(t) {
    if (t.status === 'error') return 'Error'
    if (t.status === 'disabled') return `Disabled`
    if (t.status === 'connected') return 'Connected'
    return t.status
  }

  function toolCount(t) {
    if (t.tool_names && t.tool_names.length > 0) return `${t.tool_names.length} tool${t.tool_names.length !== 1 ? 's' : ''}`
    return null
  }

  function toolEndpoint(t) {
    return t.url || t.command || ''
  }

  function isErrorTool(t) {
    return t.status === 'error' || t.status === 'disabled'
  }

  $: connectedTools = tools.filter(t => !isErrorTool(t))
  $: errorTools = tools.filter(t => isErrorTool(t))

  onMount(loadData)
</script>

<div class="page">
  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  <!-- MCP Tools Section -->
  <div class="section">
    <div class="page-header">
      <h1>MCP Tools</h1>
      <button class="btn-primary" onclick={openAddToolForm}>+ Add Tool</button>
    </div>

    {#snippet toolFormFields()}
      {#if loadingToolConfig}
        <p class="muted">Loading configuration...</p>
      {:else}
        <label>
          Name
          <input type="text" bind:value={toolName} placeholder="e.g. web-search" disabled={!!editingToolName} />
        </label>
        <label>
          Transport
          <select bind:value={toolTransport}>
            <option value="stdio">Stdio (local subprocess)</option>
            <option value="sse">SSE (remote HTTP)</option>
          </select>
        </label>
        {#if toolTransport === 'stdio'}
          <label>
            Command
            <input type="text" bind:value={toolCommand} placeholder="Path to MCP server binary" />
          </label>
          <label>
            Arguments <span class="hint">(space-separated)</span>
            <input type="text" bind:value={toolArgs} placeholder="--provider tavily" />
          </label>
        {:else}
          <label>
            URL
            <input type="text" bind:value={toolURL} placeholder="https://mcp-server.example.com/sse" />
          </label>
          <div class="env-section">
            <div class="env-header">
              <span class="env-label">Headers</span>
              <button class="btn-sm" onclick={() => addKVPair('tool-headers')}>+ Add</button>
            </div>
            {#each toolHeaderPairs as pair, i}
              <div class="env-row">
                <input type="text" bind:value={pair.key} placeholder="Header name" />
                <input type="text" bind:value={pair.value} placeholder="Value (supports ${VAR})" />
                <button class="btn-sm danger" onclick={() => removeKVPair('tool-headers', i)}>x</button>
              </div>
            {/each}
          </div>
          <label>
            Request timeout <span class="hint">(seconds, optional)</span>
            <input type="number" bind:value={toolTimeoutSecs} placeholder="30" min="1" />
          </label>
        {/if}
        <div class="env-section">
          <div class="env-header">
            <span class="env-label">Environment Variables</span>
            <button class="btn-sm" onclick={() => addKVPair('tool-env')}>+ Add</button>
          </div>
          {#each toolEnvPairs as pair, i}
            <div class="env-row">
              <input type="text" bind:value={pair.key} placeholder="Key" />
              <input type="text" bind:value={pair.value} placeholder="Value" />
              <button class="btn-sm danger" onclick={() => removeKVPair('tool-env', i)}>x</button>
            </div>
          {/each}
        </div>
        <div class="form-actions">
          <button class="btn-primary" onclick={submitToolForm} disabled={addingTool || !toolFormValid()}>
            {#if addingTool}
              {editingToolName ? 'Saving...' : 'Adding...'}
            {:else}
              {editingToolName ? 'Save Changes' : 'Add Tool'}
            {/if}
          </button>
          <button class="btn-ghost" onclick={() => { showToolForm = false; resetToolForm() }}>Cancel</button>
        </div>
      {/if}
    {/snippet}

    <!-- Add tool panel (top-level, only for adding new tools) -->
    <div class="inline-panel" class:open={showToolForm && !editingToolName}>
      <div class="inline-panel-inner">
        <div class="inline-form">
          <h2 class="form-title">Add MCP Tool</h2>
          {#if showToolForm && !editingToolName}
            {@render toolFormFields()}
          {/if}
        </div>
      </div>
    </div>

    {#if loading}
      <p class="muted">Loading...</p>
    {:else if tools.length === 0}
      <p class="muted">No MCP tools configured. Add one to extend your agent's capabilities.</p>
    {:else}
      {#if connectedTools.length > 0}
        <h3 class="group-heading">Connected</h3>
        <div class="tool-cards">
          {#each connectedTools as t}
            <div class="tool-card">
              <div class="tool-card-row">
                <span class="status-dot green"></span>
                <span class="tool-name">{t.name}</span>
                <span class="tool-endpoint mono">{toolEndpoint(t)}</span>
                <span class="status-badge connected">{statusLabel(t)}</span>
                {#if toolCount(t)}
                  <button class="tool-count-link" onclick={() => openDefsModal(t.name, t.tool_names?.length || 0)}>{toolCount(t)}</button>
                {:else}
                  <span class="tool-count">{'\u2014'}</span>
                {/if}
                <div class="tool-actions">
                  <button class="btn-ghost btn-card" onclick={() => openEditToolForm(t.name)}>Edit</button>
                  <button class="btn-ghost btn-card" onclick={() => { confirmRemove = { kind: 'tool', name: t.name } }}>Remove</button>
                </div>
              </div>
              {#if editingToolName === t.name}
                <div class="tool-card-edit">
                  <div class="inline-form">
                    {@render toolFormFields()}
                  </div>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}

      {#if errorTools.length > 0}
        <h3 class="group-heading">Errors</h3>
        <div class="tool-cards">
          {#each errorTools as t}
            <div class="tool-card error">
              <div class="tool-card-row">
                <span class="status-dot red"></span>
                <span class="tool-name">{t.name}</span>
                <span class="tool-endpoint mono">{toolEndpoint(t)}</span>
                <span class="status-badge error">{statusLabel(t)}</span>
                <span class="tool-count">{'\u2014'}</span>
                <div class="tool-actions">
                  <button class="btn-ghost btn-card" onclick={() => openEditToolForm(t.name)}>Edit</button>
                  <button class="btn-ghost btn-card" onclick={() => { confirmRemove = { kind: 'tool', name: t.name } }}>Remove</button>
                </div>
              </div>
              {#if editingToolName === t.name}
                <div class="tool-card-edit">
                  <div class="inline-form">
                    {@render toolFormFields()}
                  </div>
                </div>
              {:else if t.last_error}
                <div class="tool-error-detail">
                  <div class="tool-error-icon">
                    <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.5"/><path d="M8 4.5v4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="11" r="0.75" fill="currentColor"/></svg>
                  </div>
                  <div class="tool-error-content">
                    <div class="tool-error-title">Connection failed</div>
                    <code class="tool-error-msg">{t.last_error}</code>
                    <button class="btn-ghost btn-retry" onclick={() => restartTool(t.name)} disabled={restartingTool === t.name}>
                      {restartingTool === t.name ? 'Retrying...' : 'Retry connection'}
                    </button>
                  </div>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    {/if}
  </div>

  <!-- Plugins Section -->
  <div class="section">
    <div class="page-header">
      <h1>Plugins</h1>
      <button class="btn-primary" onclick={() => { showPluginForm = !showPluginForm }}>+ Add Plugin</button>
    </div>

    <!-- Inline form -->
    <div class="inline-panel" class:open={showPluginForm}>
      <div class="inline-panel-inner">
        <div class="inline-form">
          <h2 class="form-title">Add Plugin</h2>
          <label>
            Name
            <input type="text" bind:value={pluginName} placeholder="e.g. code-runner" />
          </label>
          <label>
            Type
            <select bind:value={pluginType}>
              <option value="subprocess">Subprocess</option>
              <option value="docker">Docker</option>
            </select>
          </label>
          {#if pluginType === 'subprocess'}
            <label>
              Command
              <input type="text" bind:value={pluginCommand} placeholder="Path to plugin binary" />
            </label>
          {:else}
            <label>
              Image
              <input type="text" bind:value={pluginImage} placeholder="e.g. ghcr.io/org/plugin:latest" />
            </label>
            <label>
              Command override <span class="hint">(optional)</span>
              <input type="text" bind:value={pluginCommand} placeholder="Entrypoint override" />
            </label>
          {/if}
          <label>
            Arguments <span class="hint">(space-separated)</span>
            <input type="text" bind:value={pluginArgs} placeholder="" />
          </label>
          <div class="env-section">
            <div class="env-header">
              <span class="env-label">Environment Variables</span>
              <button class="btn-sm" onclick={() => addKVPair('plugin')}>+ Add</button>
            </div>
            {#each pluginEnvPairs as pair, i}
              <div class="env-row">
                <input type="text" bind:value={pair.key} placeholder="Key" />
                <input type="text" bind:value={pair.value} placeholder="Value" />
                <button class="btn-sm danger" onclick={() => removeKVPair('plugin', i)}>x</button>
              </div>
            {/each}
          </div>
          {#if pluginType === 'docker'}
            <details class="resource-limits">
              <summary>Resource Limits</summary>
              <label>
                Memory limit
                <input type="text" bind:value={pluginMemoryLimit} placeholder="e.g. 256m" />
              </label>
              <label>
                CPU limit
                <input type="text" bind:value={pluginCPULimit} placeholder="e.g. 0.5" />
              </label>
              <label>
                Network mode
                <input type="text" bind:value={pluginNetwork} placeholder="none (default)" />
              </label>
            </details>
          {/if}
          <div class="form-actions">
            <button class="btn-primary" onclick={addPlugin} disabled={addingPlugin || !pluginName.trim()}>
              {addingPlugin ? 'Adding...' : 'Add Plugin'}
            </button>
            <button class="btn-ghost" onclick={() => showPluginForm = false}>Cancel</button>
          </div>
        </div>
      </div>
    </div>

    {#if loading}
      <p class="muted">Loading...</p>
    {:else if plugins.length === 0}
      <p class="muted">No plugins configured.</p>
    {:else}
      <table class="table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Image / Command</th>
            <th>Status</th>
            <th>Tools</th>
            <th class="col-actions">Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each plugins as p}
            <tr>
              <td class="mono">{p.name}</td>
              <td>{p.type}</td>
              <td class="mono truncate" title={p.image || p.command}>{p.image || p.command || '--'}</td>
              <td>
                <span class="status-dot {statusDot(p.status)}"></span>
                {p.status}
              </td>
              <td>
                {#if p.tool_names && p.tool_names.length > 0}
                  {#each p.tool_names as tn}
                    <span class="pill">{tn}</span>
                  {/each}
                {:else}
                  <span class="muted">--</span>
                {/if}
              </td>
              <td class="col-actions">
                <div class="actions">
                  <button class="btn-sm danger" onclick={() => { confirmRemove = { kind: 'plugin', name: p.name } }}>
                    Remove
                  </button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>

<!-- Tool Definitions Modal -->
{#if defsModal}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) defsModal = null }} role="dialog" aria-modal="true">
    <div class="defs-modal">
      <div class="defs-header">
        <div>
          <h2 class="defs-title">Tools &mdash; {defsModal.serverName}</h2>
          <span class="defs-subtitle">{defsModal.totalCount} tool{defsModal.totalCount !== 1 ? 's' : ''} available</span>
        </div>
        <button class="btn-ghost btn-card defs-close" onclick={() => defsModal = null} aria-label="Close">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
        </button>
      </div>

      <div class="defs-filter-wrap">
        <input type="text" class="defs-filter" bind:value={defsFilter} placeholder="Filter tools..." />
      </div>

      <div class="defs-body">
        {#if defsLoading}
          <p class="muted">Loading tool definitions...</p>
        {:else if filteredDefs.length === 0}
          <p class="muted">No tools match your filter.</p>
        {:else}
          {#each groupedDefs as [category, catTools]}
            <div class="defs-category">
              <h3 class="defs-category-title">{category}</h3>
              {#each catTools as td}
                <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
                <div class="defs-tool" class:expanded={expandedTool === td.name} onclick={() => toggleTool(td.name)}>
                  <div class="defs-tool-row">
                    <div class="defs-tool-icon" data-cat={categoryIcon(category)}>
                      {#if category === 'Search'}
                        <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><circle cx="7" cy="7" r="4.5" stroke="currentColor" stroke-width="1.5"/><path d="M10.5 10.5L14 14" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
                      {:else if category === 'Communication'}
                        <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><rect x="1.5" y="3" width="13" height="9" rx="1.5" stroke="currentColor" stroke-width="1.5"/><path d="M2 4l6 4 6-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
                      {:else}
                        <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><rect x="3" y="1.5" width="10" height="13" rx="1.5" stroke="currentColor" stroke-width="1.5"/><path d="M6 5h4M6 8h4M6 11h2" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/></svg>
                      {/if}
                    </div>
                    <div class="defs-tool-info">
                      <div class="defs-tool-name-row">
                        <span class="defs-chevron" class:open={expandedTool === td.name}>
                          <svg width="10" height="10" viewBox="0 0 10 10" fill="none"><path d="M3.5 2L7 5L3.5 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
                        </span>
                        <code class="defs-tool-name">{td.name}</code>
                      </div>
                      {#if td.description}
                        <div class="defs-tool-desc">{td.description}</div>
                      {/if}
                      <div class="defs-tool-tags">
                        <span class="defs-tag {inferAccessTag(td)}">{inferAccessTag(td)}</span>
                      </div>
                    </div>
                  </div>
                  {#if expandedTool === td.name}
                    {@const params = extractParams(td)}
                    {#if params.length > 0}
                      <div class="defs-params">
                        <table class="defs-params-table">
                          <tbody>
                            {#each params as p}
                              <tr>
                                <td class="defs-param-name"><code>{p.name}</code></td>
                                <td class="defs-param-type">{p.type}</td>
                                <td class="defs-param-desc">{p.description}</td>
                                <td class="defs-param-req">
                                  {#if p.required}
                                    <span class="defs-req-badge required">required</span>
                                  {:else}
                                    <span class="defs-req-badge optional">optional</span>
                                  {/if}
                                </td>
                              </tr>
                            {/each}
                          </tbody>
                        </table>
                      </div>
                    {:else}
                      <div class="defs-params">
                        <p class="muted" style="margin: 0; font-size: 12px;">No parameters</p>
                      </div>
                    {/if}
                  {/if}
                </div>
              {/each}
            </div>
          {/each}
        {/if}
      </div>

      <div class="defs-footer">
        <span class="muted">Showing {filteredDefs.length} of {defsTools.length} tools</span>
        <button class="btn-ghost btn-card" onclick={() => defsModal = null}>Done</button>
      </div>
    </div>
  </div>
{/if}

<!-- Remove Confirmation Modal -->
{#if confirmRemove}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmRemove = null }} role="dialog" aria-modal="true">
    <div class="confirm-modal">
      <h2>Remove {confirmRemove.kind === 'tool' ? 'Tool' : 'Plugin'}</h2>
      <p>
        Remove <strong>{confirmRemove.name}</strong>?
        {#if confirmRemove.kind === 'tool'}
          This will stop the MCP server process.
        {:else}
          This will stop the plugin process or container.
        {/if}
      </p>
      <div class="modal-actions">
        <button class="btn-danger" onclick={doRemove} disabled={removing}>
          {removing ? 'Removing...' : 'Remove'}
        </button>
        <button class="btn-ghost" onclick={() => confirmRemove = null}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page { max-width: 1000px; }
  .section { margin-bottom: 40px; }
  h1 { font-size: 20px; font-weight: 600; }

  .form-title { font-size: 16px; font-weight: 600; margin-bottom: 16px; }

  .env-section { margin-bottom: 16px; }
  .env-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
  .env-label { font-size: 13px; color: var(--text-muted); }
  .env-row { display: flex; gap: 6px; margin-bottom: 6px; }
  .env-row input { flex: 1; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); color: var(--text); padding: 6px 10px; font-size: 13px; }
  .env-row input:focus { outline: none; border-color: var(--accent); }

  .resource-limits { margin-bottom: 16px; }
  .resource-limits summary { cursor: pointer; color: var(--text-muted); font-size: 13px; margin-bottom: 12px; }
  .resource-limits summary:hover { color: var(--text); }
  .col-actions { width: 1%; white-space: nowrap; }
  .actions { display: flex; gap: 6px; }

  /* Tool cards grouped layout */
  .group-heading {
    font-size: 14px;
    font-weight: 600;
    margin: 20px 0 10px;
    color: var(--text);
  }
  .group-heading:first-of-type { margin-top: 0; }

  .tool-cards {
    display: flex;
    flex-direction: column;
    gap: 8px;
    margin-bottom: 24px;
  }

  .tool-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }
  .tool-card.error {
    border-color: var(--danger);
    border-color: rgba(196, 58, 58, 0.35);
  }

  .tool-card-row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 14px 16px;
  }

  .tool-name {
    font-weight: 600;
    font-size: 14px;
    white-space: nowrap;
    min-width: 100px;
  }

  .tool-endpoint {
    flex: 1;
    font-size: 13px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }

  .status-badge {
    font-size: 12px;
    font-weight: 500;
    padding: 3px 10px;
    border-radius: 4px;
    white-space: nowrap;
  }
  .status-badge.connected {
    background: rgba(61, 143, 98, 0.12);
    color: var(--success);
  }
  .status-badge.error {
    background: rgba(196, 58, 58, 0.1);
    color: var(--danger);
  }

  .tool-count {
    font-size: 13px;
    color: var(--text-muted);
    white-space: nowrap;
    min-width: 50px;
  }

  .tool-actions {
    display: flex;
    gap: 6px;
    flex-shrink: 0;
  }

  .btn-card {
    padding: 5px 14px;
    font-size: 13px;
  }

  /* Inline edit form inside card */
  .tool-card-edit {
    border-top: 1px solid var(--border);
    padding: 0;
  }
  .tool-card-edit .inline-form {
    border: none;
    border-radius: 0;
  }

  /* Error detail area */
  .tool-error-detail {
    display: flex;
    gap: 10px;
    padding: 12px 16px;
    background: rgba(196, 58, 58, 0.06);
    border-top: 1px solid rgba(196, 58, 58, 0.15);
  }
  .tool-error-icon {
    color: var(--danger);
    flex-shrink: 0;
    margin-top: 1px;
  }
  .tool-error-content {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }
  .tool-error-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--danger);
  }
  .tool-error-msg {
    font-size: 12px;
    color: var(--danger);
    opacity: 0.8;
    word-break: break-all;
    line-height: 1.5;
  }
  .btn-retry {
    align-self: flex-start;
    margin-top: 4px;
    font-size: 13px;
    padding: 5px 14px;
  }

  /* Clickable tool count link */
  .tool-count-link {
    background: none;
    border: none;
    font-size: 13px;
    color: var(--text-muted);
    cursor: pointer;
    white-space: nowrap;
    min-width: 50px;
    padding: 0;
    text-decoration: underline;
    text-decoration-style: dotted;
    text-underline-offset: 3px;
  }
  .tool-count-link:hover { color: var(--text); }

  /* Tool Definitions Modal */
  .defs-modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    width: 560px;
    max-width: 90vw;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    animation: scale-in 0.15s ease;
  }
  .defs-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    padding: 20px 24px 16px;
    border-bottom: 1px solid var(--border);
  }
  .defs-title {
    font-size: 18px;
    font-weight: 600;
    margin: 0;
  }
  .defs-subtitle {
    font-size: 13px;
    color: var(--text-muted);
  }
  .defs-close {
    padding: 4px 6px;
    margin: -4px -6px 0 0;
  }
  .defs-filter-wrap {
    padding: 12px 24px;
    border-bottom: 1px solid var(--border);
  }
  .defs-filter {
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 8px 12px;
    font-size: 14px;
  }
  .defs-filter:focus { outline: none; border-color: var(--accent); }
  .defs-filter::placeholder { color: var(--text-muted); }

  .defs-body {
    overflow-y: auto;
    padding: 8px 24px 16px;
    flex: 1;
    min-height: 0;
  }
  .defs-category { margin-bottom: 8px; }
  .defs-category-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-muted);
    margin: 16px 0 8px;
    padding-bottom: 4px;
    border-bottom: 1px solid var(--border);
  }
  .defs-category:first-child .defs-category-title { margin-top: 8px; }

  .defs-tool {
    border-radius: 6px;
    cursor: pointer;
    transition: background 0.1s;
    margin-bottom: 2px;
  }
  .defs-tool:hover { background: var(--hover-overlay); }
  .defs-tool.expanded {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding-bottom: 4px;
  }

  .defs-tool-row {
    display: flex;
    gap: 12px;
    padding: 10px 8px;
  }

  .defs-tool-icon {
    flex-shrink: 0;
    width: 32px;
    height: 32px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--border);
    border-radius: 6px;
    color: var(--text-muted);
    margin-top: 2px;
  }
  .defs-tool-info {
    flex: 1;
    min-width: 0;
  }
  .defs-tool-name-row {
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .defs-chevron {
    display: inline-flex;
    color: var(--text-muted);
    transition: transform 0.15s ease;
    flex-shrink: 0;
  }
  .defs-chevron.open {
    transform: rotate(90deg);
  }
  .defs-tool-name {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
  }
  .defs-tool-desc {
    font-size: 13px;
    color: var(--text-muted);
    line-height: 1.5;
    margin-top: 2px;
  }
  .defs-tool-tags {
    margin-top: 6px;
    display: flex;
    gap: 4px;
  }

  /* Params accordion panel */
  .defs-params {
    margin: 0 8px 8px 52px;
    padding: 10px 12px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 4px;
  }
  .defs-params-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }
  .defs-params-table td {
    padding: 6px 8px;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  .defs-params-table tr:last-child td { border-bottom: none; }
  .defs-param-name code {
    font-weight: 600;
    font-size: 13px;
    white-space: nowrap;
  }
  .defs-param-type {
    color: var(--text-muted);
    font-family: monospace;
    font-size: 12px;
    white-space: nowrap;
  }
  .defs-param-desc {
    color: var(--text-muted);
    line-height: 1.4;
  }
  .defs-param-req {
    white-space: nowrap;
    text-align: right;
  }
  .defs-req-badge {
    font-size: 11px;
    font-weight: 500;
    padding: 1px 6px;
    border-radius: 3px;
  }
  .defs-req-badge.required {
    background: rgba(200, 126, 48, 0.12);
    color: var(--warn);
  }
  .defs-req-badge.optional {
    color: var(--text-muted);
  }
  .defs-tag {
    display: inline-block;
    font-size: 11px;
    font-weight: 500;
    padding: 1px 8px;
    border-radius: 4px;
  }
  .defs-tag.read {
    background: rgba(61, 143, 98, 0.1);
    color: var(--success);
  }
  .defs-tag.write {
    background: rgba(200, 126, 48, 0.12);
    color: var(--warn);
  }

  .defs-footer {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 12px 24px;
    border-top: 1px solid var(--border);
    font-size: 13px;
  }
</style>
