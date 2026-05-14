<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import KebabMenu from '../components/KebabMenu.svelte'

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
  let toolKeepAliveSecs = ''
  let toolEnvPairs = []
  let toolAuth = ''         // '' or 'oauth'
  let toolClientID = ''
  let toolClientSecret = ''
  let toolScopes = ''
  let showOAuthAdvanced = false
  let showConnSettings = false
  let toolAllowLoopback = false
  let addingTool = false
  let loadingToolConfig = false

  // OAuth connect flow
  let connectingOAuth = null // tool name currently connecting
  let oauthPollInterval = null

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
  let defsTools = [] // { name, description, parameters, disabled }
  let defsFilter = ''
  let expandedTool = null // tool name currently expanded, single-expand

  // Disabled tools state (working copy in modal)
  let disabledToolsSet = new Set()
  let originalDisabledSet = new Set()
  let savingDisabledTools = false

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
  $: defsEnabledCount = defsTools.length - disabledToolsSet.size
  $: defsDirty = !setsEqual(disabledToolsSet, originalDisabledSet)

  function setsEqual(a, b) {
    if (a.size !== b.size) return false
    for (const v of a) { if (!b.has(v)) return false }
    return true
  }

  async function openDefsModal(serverName, totalCount) {
    defsModal = { serverName, totalCount }
    defsFilter = ''
    expandedTool = null
    defsLoading = true
    defsTools = []
    disabledToolsSet = new Set()
    originalDisabledSet = new Set()
    try {
      const res = await api.toolDefs(serverName)
      defsTools = res.tools || []
      const initial = new Set(defsTools.filter(t => t.disabled).map(t => t.name))
      disabledToolsSet = new Set(initial)
      originalDisabledSet = new Set(initial)
    } catch (e) {
      error = e.message
      defsModal = null
    } finally {
      defsLoading = false
    }
  }

  function toggleToolEnabled(toolName) {
    disabledToolsSet = new Set(disabledToolsSet)
    if (disabledToolsSet.has(toolName)) {
      disabledToolsSet.delete(toolName)
    } else {
      disabledToolsSet.add(toolName)
    }
  }

  function toggleCategoryEnabled(catTools) {
    const allDisabled = catTools.every(t => disabledToolsSet.has(t.name))
    disabledToolsSet = new Set(disabledToolsSet)
    for (const t of catTools) {
      if (allDisabled) {
        disabledToolsSet.delete(t.name)
      } else {
        disabledToolsSet.add(t.name)
      }
    }
  }

  function enableAllTools() {
    disabledToolsSet = new Set()
  }

  function disableAllTools() {
    disabledToolsSet = new Set(defsTools.map(t => t.name))
  }

  async function saveDisabledTools() {
    savingDisabledTools = true
    try {
      await api.updateDisabledTools(defsModal.serverName, [...disabledToolsSet])
      originalDisabledSet = new Set(disabledToolsSet)
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      savingDisabledTools = false
    }
  }

  function cancelDisabledTools() {
    disabledToolsSet = new Set(originalDisabledSet)
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
    toolURL = ''; toolHeaderPairs = []; toolTimeoutSecs = ''; toolKeepAliveSecs = ''; toolEnvPairs = []
    toolAuth = ''; toolClientID = ''; toolClientSecret = ''; toolScopes = ''
    showOAuthAdvanced = false
    showConnSettings = false
    toolAllowLoopback = false
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
      toolKeepAliveSecs = info.sse_keep_alive_secs ? String(info.sse_keep_alive_secs) : ''
      toolEnvPairs = objToEnvPairs(info.env)
      toolAuth = info.auth || ''
      toolClientID = info.client_id || ''
      toolClientSecret = '' // never returned by API
      toolScopes = (info.scopes || []).join(', ')
      toolAllowLoopback = !!info.allow_loopback
    } catch (e) {
      error = e.message
      showToolForm = false
      editingToolName = null
    } finally {
      loadingToolConfig = false
    }
  }

  $: toolFormIsValid = (() => {
    if (!toolName.trim()) return false
    if (toolTransport === 'stdio' && !toolCommand.trim()) return false
    if (toolTransport === 'sse' && !toolURL.trim()) return false
    return true
  })()

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
    const keepAlive = parseInt(toolKeepAliveSecs, 10)
    if (keepAlive > 0) cfg.sse_keep_alive_secs = keepAlive
    if (toolAuth === 'oauth') {
      cfg.auth = 'oauth'
      if (toolClientID.trim()) cfg.client_id = toolClientID.trim()
      if (toolClientSecret.trim()) cfg.client_secret = toolClientSecret.trim()
      const scopes = toolScopes.split(',').map(s => s.trim()).filter(Boolean)
      if (scopes.length > 0) cfg.scopes = scopes
    }
    if (toolAllowLoopback) cfg.allow_loopback = true
    return cfg
  }

  async function submitToolForm() {
    if (!toolFormIsValid) return
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

  async function startOAuthConnect(name) {
    connectingOAuth = name
    error = ''
    try {
      const result = await api.toolOAuthConnect(name)
      if (result.auth_url) {
        const popup = window.open(result.auth_url, 'oauth-' + name, 'width=600,height=700')
        if (!popup || popup.closed) {
          error = 'Popup was blocked by your browser. Please allow popups for this site and try again.'
          connectingOAuth = null
          return
        }
        // Poll for completion.
        let attempts = 0
        let consecutiveErrors = 0
        oauthPollInterval = setInterval(async () => {
          attempts++
          if (attempts > 150) { // 5 min at 2s intervals
            clearInterval(oauthPollInterval)
            oauthPollInterval = null
            connectingOAuth = null
            error = 'OAuth authorization timed out. Please try again.'
            return
          }
          try {
            const status = await api.toolOAuthStatus(name)
            consecutiveErrors = 0
            if (status.has_token) {
              clearInterval(oauthPollInterval)
              oauthPollInterval = null
              connectingOAuth = null
              await loadData()
            }
          } catch {
            consecutiveErrors++
            if (consecutiveErrors >= 5) {
              clearInterval(oauthPollInterval)
              oauthPollInterval = null
              connectingOAuth = null
              error = 'Lost connection while waiting for authorization. Please try again.'
            }
          }
        }, 2000)
      }
    } catch (e) {
      error = e.message
      connectingOAuth = null
    }
  }

  async function revokeOAuthToken(name) {
    error = ''
    try {
      await api.toolOAuthRevoke(name)
      await loadData()
    } catch (e) {
      error = e.message
    }
  }

  let togglingTool = null

  async function enableTool(name) {
    togglingTool = name
    error = ''
    try {
      await api.enableTool(name)
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      togglingTool = null
    }
  }

  async function disableTool(name) {
    togglingTool = name
    error = ''
    try {
      await api.disableTool(name)
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      togglingTool = null
    }
  }

  function statusDot(status) {
    if (status === 'connected') return 'green'
    if (status === 'pending_auth') return 'orange'
    if (status === 'config_error') return 'red'
    if (status === 'error') return 'red'
    if (status === 'disabled') return 'grey'
    return 'grey'
  }

  function statusLabel(t) {
    if (t.status === 'config_error') return 'Config Error'
    if (t.status === 'error') return 'Error'
    if (t.status === 'disabled') return 'Disabled'
    if (t.status === 'pending_auth') return 'Needs Auth'
    if (t.status === 'connected') return 'Connected'
    return t.status
  }

  function toolCount(t) {
    if (!t.tool_names || t.tool_names.length === 0) return null
    if (t.enabled_count < t.total_tool_count) {
      return `${t.enabled_count}/${t.total_tool_count} tools`
    }
    return `${t.tool_names.length} tool${t.tool_names.length !== 1 ? 's' : ''}`
  }

  function toolEndpoint(t) {
    return t.url || t.command || ''
  }

  function isErrorTool(t) {
    return t.status === 'error' || t.status === 'config_error'
  }

  function isDisabledTool(t) {
    return t.status === 'disabled'
  }

  $: connectedTools = tools.filter(t => !isErrorTool(t) && !isDisabledTool(t))
  $: disabledTools = tools.filter(t => isDisabledTool(t))
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
        {#if toolTransport === 'sse'}
          <!-- OAuth card -->
          <div class="settings-card">
            <div class="oauth-toggle-row">
              <div class="oauth-toggle-text">
                <span class="oauth-toggle-title">OAuth 2.1</span>
                <span class="oauth-toggle-desc">Authenticate via OAuth</span>
              </div>
              <label class="switch">
                <input type="checkbox" checked={toolAuth === 'oauth'} onchange={(e) => { toolAuth = e.target.checked ? 'oauth' : '' }} />
                <span class="switch-slider"></span>
              </label>
            </div>
            {#if toolAuth === 'oauth'}
              <button class="settings-card-toggle" onclick={() => { showOAuthAdvanced = !showOAuthAdvanced }}>
                <span class="settings-card-toggle-label">
                  Advanced settings
                  {#if !showOAuthAdvanced && (toolClientID.trim() || toolClientSecret.trim() || toolScopes.trim())}
                    <span class="settings-card-badge">{[toolClientID.trim(), toolClientSecret.trim(), toolScopes.trim()].filter(Boolean).length} configured</span>
                  {/if}
                </span>
                <svg class="settings-card-chevron" class:open={showOAuthAdvanced} width="16" height="16" viewBox="0 0 16 16" fill="none">
                  <path d="M4 6l4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
                </svg>
              </button>
              {#if showOAuthAdvanced}
                <div class="settings-card-body">
                  <label>
                    Client ID <span class="hint">(optional — leave empty for automatic registration)</span>
                    <input type="text" bind:value={toolClientID} placeholder="Pre-registered client ID" />
                  </label>
                  <label>
                    Client Secret <span class="hint">(optional)</span>
                    <input type="password" bind:value={toolClientSecret} placeholder="Pre-registered client secret" />
                  </label>
                  <label>
                    Scopes <span class="hint">(comma-separated, optional)</span>
                    <input type="text" bind:value={toolScopes} placeholder="repo, read:org" />
                  </label>
                </div>
              {/if}
            {/if}
          </div>
          <!-- Connection settings card -->
          <div class="settings-card">
            <button class="settings-card-toggle top" onclick={() => { showConnSettings = !showConnSettings }}>
              <span class="settings-card-toggle-title">Connection settings</span>
              <svg class="settings-card-chevron" class:open={showConnSettings} width="16" height="16" viewBox="0 0 16 16" fill="none">
                <path d="M4 6l4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </button>
            {#if showConnSettings}
              <div class="settings-card-body">
                <div class="env-section">
                  <div class="env-header">
                    <span class="env-label">Headers</span>
                    <button class="btn-sm" onclick={() => addKVPair('tool-headers')}>+ Add</button>
                  </div>
                  {#each toolHeaderPairs as pair, i}
                    <div class="env-row">
                      <input type="text" bind:value={pair.key} placeholder="Header name" />
                      <input type="text" bind:value={pair.value} placeholder={"Value (supports ${VAR})"} />
                      <button class="btn-sm danger" onclick={() => removeKVPair('tool-headers', i)}>x</button>
                    </div>
                  {/each}
                </div>
                <div class="timeouts-section">
                  <span class="env-label">Timeouts</span>
                  <div class="timeout-row">
                    <span class="timeout-label">Per-request</span>
                    <div class="timeout-input">
                      <input type="number" bind:value={toolTimeoutSecs} placeholder="30" min="1" />
                      <span class="timeout-unit">sec</span>
                    </div>
                  </div>
                  <div class="timeout-row">
                    <span class="timeout-label">Keep-alive interval</span>
                    <div class="timeout-input">
                      <input type="number" bind:value={toolKeepAliveSecs} placeholder="15" min="1" />
                      <span class="timeout-unit">sec</span>
                    </div>
                  </div>
                </div>
                <div class="unsafe-section">
                  <div class="unsafe-section-header">
                    <svg class="unsafe-icon" width="14" height="14" viewBox="0 0 16 16" fill="none">
                      <path d="M8 1L1 14h14L8 1z" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/>
                      <path d="M8 6v4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
                      <circle cx="8" cy="12" r="0.8" fill="currentColor"/>
                    </svg>
                    <span>Unsafe options</span>
                  </div>
                  <div class="unsafe-toggle-row">
                    <div class="unsafe-toggle-text">
                      <span class="unsafe-toggle-title">Allow loopback connections</span>
                      <span class="unsafe-toggle-desc">
                        Permits localhost, 127.x, and ::1 URLs. Use for sidecar MCP servers in the same pod.
                      </span>
                    </div>
                    <div class="unsafe-toggle-control">
                      <label class="switch">
                        <input type="checkbox" bind:checked={toolAllowLoopback} />
                        <span class="switch-slider"></span>
                      </label>
                      <span class="unsafe-popover-anchor">
                        <svg class="unsafe-info-icon" width="14" height="14" viewBox="0 0 16 16" fill="none">
                          <circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.2"/>
                          <path d="M8 7v4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
                          <circle cx="8" cy="5" r="0.8" fill="currentColor"/>
                        </svg>
                        <span class="unsafe-popover">
                          Disabling SSRF loopback protection allows the MCP server to access services on localhost. This is safe for trusted sidecar containers but dangerous if the tool URL can be influenced by untrusted input &mdash; an attacker could reach internal services, cloud metadata endpoints are still blocked.
                        </span>
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            {/if}
          </div>
        {/if}
        <div class="form-actions">
          <button class="btn-primary" onclick={submitToolForm} disabled={addingTool || !toolFormIsValid}>
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
        <h3 class="group-heading">Connected <span class="group-count">&middot; {connectedTools.length}</span></h3>
        <div class="tool-cards">
          {#each connectedTools as t}
            <div class="tool-card">
              <div class="tool-card-row">
                <span class="status-dot green"></span>
                <div class="tool-card-info">
                  <div class="tool-card-meta">
                    <span class="tool-name">{t.name}</span>
                    {#if t.auth_type === 'oauth'}
                      <span class="auth-tag oauth">OAuth</span>
                    {/if}
                    {#if toolCount(t)}
                      <span class="meta-sep">&middot;</span>
                      <button class="tool-count-link" onclick={() => openDefsModal(t.name, t.tool_names?.length || 0)}>{toolCount(t)}</button>
                    {/if}
                  </div>
                  <span class="tool-endpoint mono">{toolEndpoint(t)}</span>
                </div>
                <div class="tool-card-actions">
                  {#if t.auth_type === 'oauth' && !t.oauth_status?.has_token}
                    <button class="btn-primary btn-card" onclick={() => startOAuthConnect(t.name)} disabled={connectingOAuth === t.name}>
                      {connectingOAuth === t.name ? 'Connecting...' : 'Connect'}
                    </button>
                  {:else}
                    <button class="btn-ghost btn-card" onclick={() => disableTool(t.name)} disabled={togglingTool === t.name}>
                      {togglingTool === t.name ? 'Disabling...' : 'Disable'}
                    </button>
                  {/if}
                  <KebabMenu items={[
                    { label: 'Edit', onclick: () => openEditToolForm(t.name) },
                    ...(toolCount(t) ? [{ label: 'View tools', onclick: () => openDefsModal(t.name, t.tool_names?.length || 0) }] : []),
                    ...(t.auth_type === 'oauth' && t.oauth_status?.has_token ? [{ label: 'Disconnect OAuth', onclick: () => revokeOAuthToken(t.name) }] : []),
                    { separator: true },
                    { label: 'Remove', danger: true, onclick: () => { confirmRemove = { kind: 'tool', name: t.name } } },
                  ]} />
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

      {#if disabledTools.length > 0}
        <h3 class="group-heading">Disabled <span class="group-count">&middot; {disabledTools.length}</span></h3>
        <div class="tool-cards">
          {#each disabledTools as t}
            <div class="tool-card">
              <div class="tool-card-row">
                <span class="status-dot grey"></span>
                <div class="tool-card-info">
                  <div class="tool-card-meta">
                    <span class="tool-name">{t.name}</span>
                    {#if t.auth_type === 'oauth'}
                      <span class="auth-tag oauth">OAuth</span>
                    {/if}
                  </div>
                  <span class="tool-endpoint mono">{toolEndpoint(t)}</span>
                </div>
                <div class="tool-card-actions">
                  <button class="btn-primary btn-card" onclick={() => enableTool(t.name)} disabled={togglingTool === t.name}>
                    {togglingTool === t.name ? 'Enabling...' : 'Enable'}
                  </button>
                  <KebabMenu items={[
                    { label: 'Edit', onclick: () => openEditToolForm(t.name) },
                    { separator: true },
                    { label: 'Remove', danger: true, onclick: () => { confirmRemove = { kind: 'tool', name: t.name } } },
                  ]} />
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
        <h3 class="group-heading">Needs attention <span class="group-count">&middot; {errorTools.length}</span></h3>
        <div class="tool-cards">
          {#each errorTools as t}
            <div class="tool-card error">
              <div class="tool-card-row">
                <span class="status-dot red"></span>
                <div class="tool-card-info">
                  <div class="tool-card-meta">
                    <span class="tool-name">{t.name}</span>
                    {#if t.auth_type === 'oauth'}
                      <span class="auth-tag oauth">OAuth</span>
                    {/if}
                  </div>
                  <span class="tool-endpoint mono">{toolEndpoint(t)}</span>
                </div>
                <div class="tool-card-actions">
                  {#if t.auth_type === 'oauth' && t.oauth_status?.needs_reauth}
                    <button class="btn-primary btn-card" onclick={() => startOAuthConnect(t.name)} disabled={connectingOAuth === t.name}>
                      {connectingOAuth === t.name ? 'Connecting...' : 'Connect'}
                    </button>
                  {:else if t.last_error && !t.config_error}
                    <button class="btn-ghost btn-card" onclick={() => restartTool(t.name)} disabled={restartingTool === t.name}>
                      {restartingTool === t.name ? 'Retrying...' : 'Retry'}
                    </button>
                  {/if}
                  <KebabMenu items={[
                    { label: 'Edit', onclick: () => openEditToolForm(t.name) },
                    { separator: true },
                    { label: 'Remove', danger: true, onclick: () => { confirmRemove = { kind: 'tool', name: t.name } } },
                  ]} />
                </div>
              </div>
              {#if editingToolName === t.name}
                <div class="tool-card-edit">
                  <div class="inline-form">
                    {@render toolFormFields()}
                  </div>
                </div>
              {:else if t.config_error}
                <div class="tool-error-detail">
                  <div class="tool-error-icon">
                    <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><circle cx="8" cy="8" r="7" stroke="currentColor" stroke-width="1.5"/><path d="M8 4.5v4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><circle cx="8" cy="11" r="0.75" fill="currentColor"/></svg>
                  </div>
                  <div class="tool-error-content">
                    <div class="tool-error-title">Configuration error</div>
                    <code class="tool-error-msg">{t.config_error}</code>
                    <p class="tool-error-hint">Fix the configuration in denkeeper.toml and the tool will retry automatically.</p>
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
          <span class="defs-subtitle">{defsEnabledCount} of {defsTools.length} tools enabled</span>
        </div>
        <button class="btn-ghost btn-card defs-close" onclick={() => defsModal = null} aria-label="Close">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
        </button>
      </div>

      <div class="defs-filter-wrap">
        <input type="text" class="defs-filter" bind:value={defsFilter} placeholder="Filter tools..." />
        <div class="defs-bulk-actions">
          <button class="btn-ghost btn-xs" onclick={enableAllTools} disabled={disabledToolsSet.size === 0}>Enable All</button>
          <button class="btn-ghost btn-xs" onclick={disableAllTools} disabled={disabledToolsSet.size === defsTools.length}>Disable All</button>
        </div>
      </div>

      <div class="defs-body">
        {#if defsLoading}
          <p class="muted">Loading tool definitions...</p>
        {:else if filteredDefs.length === 0}
          <p class="muted">No tools match your filter.</p>
        {:else}
          {#each groupedDefs as [category, catTools]}
            {@const catEnabledCount = catTools.filter(t => !disabledToolsSet.has(t.name)).length}
            <div class="defs-category">
              <div class="defs-category-header">
                <h3 class="defs-category-title">{category} <span class="defs-category-count">{catEnabledCount}/{catTools.length}</span></h3>
                <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
                <label class="switch switch-sm" onclick={(e) => e.stopPropagation()}>
                  <input type="checkbox" checked={catEnabledCount > 0} onchange={() => toggleCategoryEnabled(catTools)} />
                  <span class="switch-slider"></span>
                </label>
              </div>
              {#each catTools as td}
                <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
                <div class="defs-tool" class:expanded={expandedTool === td.name} class:tool-disabled={disabledToolsSet.has(td.name)} onclick={() => toggleTool(td.name)}>
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
                        <span class="chevron-toggle" class:open={expandedTool === td.name}>
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
                    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
                    <label class="switch switch-sm" onclick={(e) => e.stopPropagation()}>
                      <input type="checkbox" checked={!disabledToolsSet.has(td.name)} onchange={() => toggleToolEnabled(td.name)} />
                      <span class="switch-slider"></span>
                    </label>
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
        <div class="defs-footer-actions">
          {#if defsDirty}
            <button class="btn-ghost btn-card" onclick={cancelDisabledTools} disabled={savingDisabledTools}>Cancel</button>
            <button class="btn-primary btn-card" onclick={saveDisabledTools} disabled={savingDisabledTools}>
              {savingDisabledTools ? 'Saving...' : 'Save'}
            </button>
          {:else}
            <button class="btn-ghost btn-card" onclick={() => defsModal = null}>Done</button>
          {/if}
        </div>
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
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    margin: 24px 0 10px;
    color: var(--text-muted);
  }
  .group-heading:first-of-type { margin-top: 0; }
  .group-count { font-weight: 400; }

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
    overflow: visible;
  }
  .tool-card.error {
    border-color: rgba(196, 58, 58, 0.25);
  }

  .tool-card-row {
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 16px;
  }

  .tool-card-info {
    display: flex;
    flex-direction: column;
    gap: 2px;
    flex: 1;
    min-width: 0;
  }

  .tool-card-meta {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .tool-card-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-shrink: 0;
  }

  .tool-name {
    font-weight: 600;
    font-size: 14px;
    white-space: nowrap;
  }

  .tool-endpoint {
    font-size: 12px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }

  .auth-tag {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    padding: 1px 6px;
    border-radius: 3px;
    white-space: nowrap;
    background: rgba(138, 122, 106, 0.10);
    color: var(--text-muted);
  }
  .auth-tag.oauth {
    background: rgba(234, 179, 8, 0.15);
    color: #b45309;
  }

  .meta-sep {
    color: var(--text-muted);
    font-size: 12px;
    opacity: 0.5;
  }

  /* Shared settings card (OAuth, Connection settings) */
  .settings-card {
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface);
    overflow: hidden;
  }
  .settings-card + .settings-card {
    margin-top: 10px;
  }

  .oauth-toggle-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 16px;
  }
  .oauth-toggle-text {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .oauth-toggle-title {
    font-weight: 600;
    font-size: 14px;
    color: var(--text);
  }
  .oauth-toggle-desc {
    font-size: 12px;
    color: var(--text-muted);
  }

  /* Pill toggle switch */
  .switch {
    position: relative;
    display: inline-block;
    width: 44px;
    height: 24px;
    flex-shrink: 0;
  }
  .switch input {
    opacity: 0;
    width: 0;
    height: 0;
  }
  .switch-slider {
    position: absolute;
    cursor: pointer;
    inset: 0;
    background: var(--border);
    border-radius: 24px;
    transition: background 0.2s;
  }
  .switch-slider::before {
    content: "";
    position: absolute;
    height: 18px;
    width: 18px;
    left: 3px;
    bottom: 3px;
    background: white;
    border-radius: 50%;
    transition: transform 0.2s;
  }
  .switch input:checked + .switch-slider {
    background: var(--accent);
  }
  .switch input:checked + .switch-slider::before {
    transform: translateX(20px);
  }

  /* Collapsible toggle row inside settings cards */
  .settings-card-toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 10px 16px;
    border: none;
    border-top: 1px solid var(--border);
    background: transparent;
    cursor: pointer;
    color: var(--text-muted);
    font-size: 13px;
    font-weight: 500;
  }
  .settings-card-toggle.top {
    border-top: none;
    color: var(--text);
    font-size: 14px;
    font-weight: 600;
    padding: 14px 16px;
  }
  .settings-card-toggle:hover {
    background: var(--bg);
  }
  .settings-card-toggle-label {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .settings-card-toggle-title {
    font-size: 14px;
    font-weight: 600;
  }
  .settings-card-badge {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    background: var(--bg);
    border: 1px solid var(--border);
    padding: 1px 8px;
    border-radius: 10px;
  }
  .settings-card-chevron {
    color: var(--text-muted);
    transition: transform 0.2s;
    flex-shrink: 0;
  }
  .settings-card-chevron.open {
    transform: rotate(180deg);
  }

  .settings-card-body {
    border-top: 1px solid var(--border);
    padding: 14px 16px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .timeouts-section { margin-bottom: 16px; display: flex; flex-direction: column; gap: 8px; }
  .timeout-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .timeout-label {
    font-size: 13px;
    font-weight: 500;
    color: var(--text);
    white-space: nowrap;
  }
  .timeout-input {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .timeout-input input {
    width: 80px;
    text-align: center;
  }
  .timeout-unit {
    font-size: 13px;
    color: var(--text-muted);
  }

  .btn-card {
    padding: 5px 14px;
    font-size: 13px;
  }

  /* Inline edit form inside card */
  .tool-card-edit {
    border-top: 1px solid var(--border);
    border-radius: 0 0 var(--radius) var(--radius);
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
    border-radius: 0 0 var(--radius) var(--radius);
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
  .tool-error-hint {
    font-size: 12px;
    color: var(--text-muted);
    margin: 4px 0 0;
  }

  /* Clickable tool count link */
  .tool-count-link {
    background: none;
    border: none;
    font-size: 12px;
    color: var(--text-muted);
    cursor: pointer;
    white-space: nowrap;
    padding: 0;
    text-decoration: underline;
    text-decoration-style: dashed;
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
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .defs-bulk-actions {
    display: flex;
    gap: 4px;
    flex-shrink: 0;
  }
  .btn-xs {
    font-size: 12px;
    padding: 4px 8px;
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
  .defs-category-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin: 16px 0 8px;
    padding-bottom: 4px;
    border-bottom: 1px solid var(--border);
  }
  .defs-category:first-child .defs-category-header { margin-top: 8px; }
  .defs-category-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--text-muted);
    margin: 0;
  }
  .defs-category-count {
    font-weight: 400;
    color: var(--text-muted);
    margin-left: 4px;
  }

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

  .defs-tool.tool-disabled .defs-tool-name,
  .defs-tool.tool-disabled .defs-tool-desc,
  .defs-tool.tool-disabled .defs-tool-tags {
    color: var(--text-muted);
  }

  .defs-tool-row {
    display: flex;
    gap: 12px;
    padding: 10px 8px;
    align-items: flex-start;
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
  /* Chevron rotation: uses shared .chevron-toggle from shared.css */
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
  .defs-footer-actions {
    display: flex;
    gap: 8px;
  }

  .switch-sm {
    width: 36px;
    height: 20px;
    margin-top: 4px;
  }
  .switch-sm .switch-slider::before {
    height: 14px;
    width: 14px;
  }
  .switch-sm input:checked + .switch-slider::before {
    transform: translateX(16px);
  }

  /* Unsafe options section (inside connection settings) */
  .unsafe-section {
    border-top: 1px solid var(--border);
    padding-top: 12px;
    margin-top: 12px;
  }
  .unsafe-section-header {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    font-weight: 600;
    color: #b45309;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    margin-bottom: 10px;
  }
  .unsafe-icon {
    color: #b45309;
    flex-shrink: 0;
  }
  .unsafe-toggle-row {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
  }
  .unsafe-toggle-text {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .unsafe-toggle-title {
    font-size: 13px;
    font-weight: 500;
    color: var(--text);
  }
  .unsafe-toggle-desc {
    font-size: 12px;
    color: var(--text-muted);
    line-height: 1.4;
  }
  .unsafe-toggle-control {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }
  .unsafe-info-icon {
    color: var(--text-muted);
    cursor: help;
  }
  .unsafe-popover-anchor {
    position: relative;
    display: inline-flex;
  }
  .unsafe-popover {
    display: none;
    position: absolute;
    right: 0;
    bottom: 22px;
    width: 280px;
    padding: 10px 12px;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
    z-index: 10;
  }
  .unsafe-popover-anchor:hover .unsafe-popover {
    display: block;
  }

  @media (max-width: 768px) {
    .tool-card-row > .status-dot {
      display: none;
    }
    .tool-card-meta {
      flex-wrap: wrap;
    }
    .tool-name {
      white-space: normal;
      word-break: break-word;
    }
    .tool-endpoint {
      white-space: normal;
      word-break: break-all;
    }
    .defs-params {
      margin-left: 8px;
    }
    .defs-filter-wrap {
      flex-wrap: wrap;
    }
    .defs-bulk-actions {
      width: 100%;
      justify-content: flex-end;
    }
  }
</style>
