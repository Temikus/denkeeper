<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'

  let tools = []
  let plugins = []
  let loading = true
  let error = ''

  // Add tool modal
  let showAddTool = false
  let toolName = ''
  let toolCommand = ''
  let toolArgs = ''
  let toolEnvPairs = []
  let addingTool = false

  // Add plugin modal
  let showAddPlugin = false
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

  async function addTool() {
    if (!toolName.trim() || !toolCommand.trim()) return
    addingTool = true
    error = ''
    try {
      await api.addTool(toolName.trim(), toolCommand.trim(), parseArgs(toolArgs), envPairsToObj(toolEnvPairs))
      showAddTool = false
      toolName = ''; toolCommand = ''; toolArgs = ''; toolEnvPairs = []
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
      showAddPlugin = false
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

  function addEnvPair(target) {
    if (target === 'tool') {
      toolEnvPairs = [...toolEnvPairs, { key: '', value: '' }]
    } else {
      pluginEnvPairs = [...pluginEnvPairs, { key: '', value: '' }]
    }
  }

  function removeEnvPair(target, idx) {
    if (target === 'tool') {
      toolEnvPairs = toolEnvPairs.filter((_, i) => i !== idx)
    } else {
      pluginEnvPairs = pluginEnvPairs.filter((_, i) => i !== idx)
    }
  }

  function statusDot(status) {
    if (status === 'connected') return 'dot-green'
    if (status === 'error') return 'dot-red'
    return 'dot-grey'
  }

  onMount(loadData)
</script>

<div class="page">
  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  <!-- MCP Tools Section -->
  <div class="section">
    <div class="header">
      <h1>MCP Tools</h1>
      <button class="btn-primary" onclick={() => { showAddTool = true }}>+ Add Tool</button>
    </div>

    {#if loading}
      <p class="muted">Loading...</p>
    {:else if tools.length === 0}
      <p class="muted">No MCP tools configured. Add one to extend your agent's capabilities.</p>
    {:else}
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Command</th>
            <th>Status</th>
            <th>Exposed Tools</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {#each tools as t}
            <tr>
              <td class="mono">{t.Name}</td>
              <td class="mono truncate" title={t.Command}>{t.Command}</td>
              <td>
                <span class="status {statusDot(t.Status)}"></span>
                {t.Status}
              </td>
              <td>
                {#if t.ToolNames && t.ToolNames.length > 0}
                  {#each t.ToolNames as tn}
                    <span class="pill">{tn}</span>
                  {/each}
                {:else}
                  <span class="muted">--</span>
                {/if}
              </td>
              <td>
                <button class="btn-sm danger" onclick={() => { confirmRemove = { kind: 'tool', name: t.Name } }}>
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
    <div class="header">
      <h1>Plugins</h1>
      <button class="btn-primary" onclick={() => { showAddPlugin = true }}>+ Add Plugin</button>
    </div>

    {#if loading}
      <p class="muted">Loading...</p>
    {:else if plugins.length === 0}
      <p class="muted">No plugins configured.</p>
    {:else}
      <table>
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
                <span class="status {statusDot(p.status)}"></span>
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

<!-- Add Tool Modal -->
{#if showAddTool}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) showAddTool = false }} role="dialog" aria-modal="true">
    <div class="modal">
      <h2>Add MCP Tool</h2>
      <label>
        Name
        <input type="text" bind:value={toolName} placeholder="e.g. web-search" />
      </label>
      <label>
        Command
        <input type="text" bind:value={toolCommand} placeholder="Path to MCP server binary" />
      </label>
      <label>
        Arguments <span class="hint">(space-separated)</span>
        <input type="text" bind:value={toolArgs} placeholder="--provider tavily" />
      </label>
      <div class="env-section">
        <div class="env-header">
          <span class="env-label">Environment Variables</span>
          <button class="btn-sm" onclick={() => addEnvPair('tool')}>+ Add</button>
        </div>
        {#each toolEnvPairs as pair, i}
          <div class="env-row">
            <input type="text" bind:value={pair.key} placeholder="Key" />
            <input type="text" bind:value={pair.value} placeholder="Value" />
            <button class="btn-sm danger" onclick={() => removeEnvPair('tool', i)}>x</button>
          </div>
        {/each}
      </div>
      <div class="modal-actions">
        <button class="btn-primary" onclick={addTool} disabled={addingTool || !toolName.trim() || !toolCommand.trim()}>
          {addingTool ? 'Adding...' : 'Add Tool'}
        </button>
        <button class="btn-ghost" onclick={() => showAddTool = false}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- Add Plugin Modal -->
{#if showAddPlugin}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) showAddPlugin = false }} role="dialog" aria-modal="true">
    <div class="modal wide">
      <h2>Add Plugin</h2>
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
          <button class="btn-sm" onclick={() => addEnvPair('plugin')}>+ Add</button>
        </div>
        {#each pluginEnvPairs as pair, i}
          <div class="env-row">
            <input type="text" bind:value={pair.key} placeholder="Key" />
            <input type="text" bind:value={pair.value} placeholder="Value" />
            <button class="btn-sm danger" onclick={() => removeEnvPair('plugin', i)}>x</button>
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
      <div class="modal-actions">
        <button class="btn-primary" onclick={addPlugin} disabled={addingPlugin || !pluginName.trim()}>
          {addingPlugin ? 'Adding...' : 'Add Plugin'}
        </button>
        <button class="btn-ghost" onclick={() => showAddPlugin = false}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- Remove Confirmation Modal -->
{#if confirmRemove}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmRemove = null }} role="dialog" aria-modal="true">
    <div class="modal">
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
  .header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 20px; }
  h1 { font-size: 20px; font-weight: 600; }

  .banner {
    padding: 12px 16px;
    border-radius: var(--radius);
    margin-bottom: 16px;
  }
  .banner.error { background: rgba(224,92,110,0.15); border: 1px solid var(--danger); color: var(--danger); }

  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; padding: 8px 12px; border-bottom: 1px solid var(--border); color: var(--text-muted); font-weight: 500; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
  td { padding: 10px 12px; border-bottom: 1px solid var(--border); vertical-align: middle; }

  .mono { font-family: monospace; font-size: 13px; }
  .truncate { max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .muted { color: var(--text-muted); }
  .pill { display: inline-block; background: var(--border); color: var(--text-muted); padding: 2px 6px; border-radius: 4px; font-size: 11px; margin: 2px 2px 2px 0; }

  .status { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 6px; vertical-align: middle; }
  .dot-green { background: var(--success); }
  .dot-red { background: var(--danger); }
  .dot-grey { background: var(--text-muted); }

  .btn-primary {
    background: var(--accent);
    color: #fff;
    border: none;
    padding: 8px 16px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 13px;
  }
  .btn-primary:hover:not(:disabled) { background: var(--accent-hover); }
  .btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-ghost {
    background: none;
    border: 1px solid var(--border);
    color: var(--text);
    padding: 8px 16px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 13px;
  }
  .btn-ghost:hover { border-color: var(--text-muted); }
  .btn-sm {
    background: var(--border);
    border: none;
    color: var(--text);
    padding: 4px 10px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 12px;
  }
  .btn-sm:hover { background: var(--accent); }
  .btn-sm.danger:hover { background: var(--danger); }
  .btn-danger {
    background: var(--danger);
    color: #fff;
    border: none;
    padding: 8px 16px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 13px;
  }
  .btn-danger:hover:not(:disabled) { opacity: 0.85; }
  .btn-danger:disabled { opacity: 0.5; cursor: not-allowed; }

  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 28px;
    width: 460px;
    max-width: 90vw;
  }
  .modal.wide { width: 520px; }
  .modal h2 { font-size: 16px; font-weight: 600; margin-bottom: 16px; }
  .modal p { color: var(--text-muted); margin-bottom: 20px; line-height: 1.6; }
  .modal label { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; font-size: 13px; color: var(--text-muted); }
  .modal input[type="text"],
  .modal select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 8px 12px;
    font-size: 14px;
  }
  .modal input[type="text"]:focus,
  .modal select:focus { outline: none; border-color: var(--accent); }
  .modal-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 8px; }
  .hint { font-size: 11px; color: var(--text-muted); }

  .env-section { margin-bottom: 16px; }
  .env-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
  .env-label { font-size: 13px; color: var(--text-muted); }
  .env-row { display: flex; gap: 6px; margin-bottom: 6px; }
  .env-row input { flex: 1; background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); color: var(--text); padding: 6px 10px; font-size: 13px; }
  .env-row input:focus { outline: none; border-color: var(--accent); }

  .resource-limits { margin-bottom: 16px; }
  .resource-limits summary { cursor: pointer; color: var(--text-muted); font-size: 13px; margin-bottom: 12px; }
  .resource-limits summary:hover { color: var(--text); }
</style>
