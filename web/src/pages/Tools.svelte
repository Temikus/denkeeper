<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'

  let tools = []
  let plugins = []
  let loading = true
  let error = ''

  // Add tool inline form
  let showToolForm = false
  let toolName = ''
  let toolTransport = 'stdio'
  let toolCommand = ''
  let toolArgs = ''
  let toolURL = ''
  let toolHeaderPairs = []
  let toolTimeoutSecs = ''
  let toolEnvPairs = []
  let addingTool = false

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

  function toolFormValid() {
    if (!toolName.trim()) return false
    if (toolTransport === 'stdio' && !toolCommand.trim()) return false
    if (toolTransport === 'sse' && !toolURL.trim()) return false
    return true
  }

  async function addTool() {
    if (!toolFormValid()) return
    addingTool = true
    error = ''
    try {
      const cfg = { name: toolName.trim() }
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
      await api.addTool(cfg)
      showToolForm = false
      toolName = ''; toolTransport = 'stdio'; toolCommand = ''; toolArgs = ''
      toolURL = ''; toolHeaderPairs = []; toolTimeoutSecs = ''; toolEnvPairs = []
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
    if (t.status === 'error' && t.last_error) return `error: ${t.last_error}`
    if (t.status === 'disabled') return `disabled after ${t.restart_count} restart${t.restart_count !== 1 ? 's' : ''}`
    if (t.status === 'connected' && t.restart_count > 0) return `connected (restarted ${t.restart_count}x)`
    return t.status
  }

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
      <button class="btn-primary" onclick={() => { showToolForm = !showToolForm }}>+ Add Tool</button>
    </div>

    <!-- Inline form -->
    <div class="inline-panel" class:open={showToolForm}>
      <div class="inline-panel-inner">
        <div class="inline-form">
          <h2 class="form-title">Add MCP Tool</h2>
          <label>
            Name
            <input type="text" bind:value={toolName} placeholder="e.g. web-search" />
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
            <button class="btn-primary" onclick={addTool} disabled={addingTool || !toolFormValid()}>
              {addingTool ? 'Adding...' : 'Add Tool'}
            </button>
            <button class="btn-ghost" onclick={() => showToolForm = false}>Cancel</button>
          </div>
        </div>
      </div>
    </div>

    {#if loading}
      <p class="muted">Loading...</p>
    {:else if tools.length === 0}
      <p class="muted">No MCP tools configured. Add one to extend your agent's capabilities.</p>
    {:else}
      <table class="table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Command / URL</th>
            <th>Status</th>
            <th>Exposed Tools</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each tools as t}
            <tr>
              <td class="mono">{t.name}</td>
              <td class="mono truncate" title={t.url || t.command}>{t.url || t.command}</td>
              <td title={t.last_error || ''}>
                <span class="status-dot {statusDot(t.status)}"></span>
                {statusLabel(t)}
              </td>
              <td>
                {#if t.tool_names && t.tool_names.length > 0}
                  {#each t.tool_names as tn}
                    <span class="pill">{tn}</span>
                  {/each}
                {:else}
                  <span class="muted">--</span>
                {/if}
              </td>
              <td class="actions">
                {#if t.status === 'error' || t.status === 'disabled'}
                  <button class="btn-sm" onclick={() => restartTool(t.name)} disabled={restartingTool === t.name}>
                    {restartingTool === t.name ? 'Restarting...' : 'Restart'}
                  </button>
                {/if}
                <button class="btn-sm danger" onclick={() => { confirmRemove = { kind: 'tool', name: t.name } }}>
                  Remove
                </button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
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
            <th>Actions</th>
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
              <td>
                <button class="btn-sm danger" onclick={() => { confirmRemove = { kind: 'plugin', name: p.name } }}>
                  Remove
                </button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>

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

  .truncate { max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

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
  .actions { display: flex; gap: 6px; }
</style>
