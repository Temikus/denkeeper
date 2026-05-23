<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import { timezoneGroups, allTimezones } from '../timezones.js'

  let config = $state(null)
  let loading = $state(true)
  let error = $state('')

  // External URL editing
  let editing = $state(false)
  let editValue = $state('')
  let saving = $state(false)
  let saveOk = $state(false)

  // Process control
  let reloading = $state(false)
  let reloadOk = $state(false)
  let restarting = $state(false)
  let confirmRestart = $state(false)

  // Timezone editing
  let editingTz = $state(false)
  let tzValue = $state('')
  let tzCustom = $state(false)
  let tzFilter = $state('')
  let savingTz = $state(false)
  let saveTzOk = $state(false)

  // MCP Server
  let savingMcp = $state(false)
  let saveMcpOk = $state(false)
  let mcpCopied = $state(false)

  async function fetchConfig() {
    loading = true
    error = ''
    try {
      config = await api.serverConfig()
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function startEdit() {
    editValue = config.external_url || ''
    editing = true
  }

  function cancelEdit() {
    editing = false
  }

  async function saveExternalURL() {
    saving = true
    error = ''
    try {
      await api.updateServerConfig({ external_url: editValue })
      config.external_url = editValue
      editing = false
      saveOk = true
      setTimeout(() => { saveOk = false }, 3000)
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  function startEditTz() {
    tzValue = config.timezone || 'UTC'
    tzCustom = !allTimezones.includes(tzValue)
    tzFilter = ''
    editingTz = true
  }

  function cancelEditTz() {
    editingTz = false
  }

  async function saveTimezone() {
    savingTz = true
    error = ''
    try {
      await api.updateServerConfig({ timezone: tzValue })
      config.timezone = tzValue
      editingTz = false
      saveTzOk = true
      setTimeout(() => { saveTzOk = false }, 3000)
    } catch (e) {
      error = e.message
    } finally {
      savingTz = false
    }
  }

  function filteredGroups() {
    if (!tzFilter) return timezoneGroups
    const q = tzFilter.toLowerCase()
    return timezoneGroups
      .map(g => ({ ...g, zones: g.zones.filter(z => z.toLowerCase().includes(q)) }))
      .filter(g => g.zones.length > 0)
  }

  async function toggleMcp() {
    savingMcp = true
    error = ''
    try {
      const enabled = !config.mcp_server_enabled
      await api.updateServerConfig({ mcp_server_enabled: enabled })
      config.mcp_server_enabled = enabled
      saveMcpOk = true
      setTimeout(() => { saveMcpOk = false }, 3000)
    } catch (e) {
      error = e.message
    } finally {
      savingMcp = false
    }
  }

  async function saveMcpField(field, value) {
    savingMcp = true
    error = ''
    try {
      await api.updateServerConfig({ [field]: value })
      config[field] = value
      saveMcpOk = true
      setTimeout(() => { saveMcpOk = false }, 3000)
    } catch (e) {
      error = e.message
    } finally {
      savingMcp = false
    }
  }

  function copyEndpoint() {
    if (config?.mcp_server_endpoint) {
      navigator.clipboard.writeText(config.mcp_server_endpoint)
      mcpCopied = true
      setTimeout(() => { mcpCopied = false }, 2000)
    }
  }

  async function reloadConfig() {
    reloading = true
    error = ''
    try {
      await api.reloadConfig()
      reloadOk = true
      setTimeout(() => { reloadOk = false }, 3000)
      await fetchConfig()
    } catch (e) {
      error = e.message
    } finally {
      reloading = false
    }
  }

  async function restartProcess() {
    restarting = true
    error = ''
    try {
      await api.restartProcess()
      confirmRestart = false
    } catch (e) {
      error = e.message
      restarting = false
    }
  }

  onMount(fetchConfig)
</script>

<h1 class="page-title">Server</h1>
<ErrorBanner message={error} />

{#if loading && !config}
  <p class="loading">Loading...</p>
{:else if config}
  <h2 class="section-title">General</h2>
  <div class="config-card">
    <div class="config-row">
      <div class="config-label">
        <div class="config-name">Timezone</div>
        <div class="config-desc">
          IANA timezone used for evaluating cron schedule expressions.
          Changes take effect after restart.
        </div>
      </div>
      {#if !editingTz}
        <div class="config-value-row">
          <span class="config-value mono">{config.timezone || 'UTC'}</span>
          <button class="btn btn-sm" onclick={startEditTz}>Edit</button>
        </div>
      {/if}
    </div>
    {#if editingTz}
      <div class="config-edit">
        {#if !tzCustom}
          <input
            type="text"
            class="input tz-filter"
            bind:value={tzFilter}
            placeholder="Filter timezones..."
          />
          <select class="input tz-select" bind:value={tzValue} size="8">
            {#each filteredGroups() as group}
              <optgroup label={group.label}>
                {#each group.zones as zone}
                  <option value={zone}>{zone}</option>
                {/each}
              </optgroup>
            {/each}
          </select>
          <button class="btn-link" onclick={() => { tzCustom = true }}>Enter custom value</button>
        {:else}
          <input
            type="text"
            class="input"
            bind:value={tzValue}
            placeholder="e.g. America/New_York"
          />
          <button class="btn-link" onclick={() => { tzCustom = false }}>Back to list</button>
        {/if}
        <div class="config-actions">
          <button class="btn btn-primary" onclick={saveTimezone} disabled={savingTz || !tzValue.trim()}>
            {savingTz ? 'Saving...' : 'Save'}
          </button>
          <button class="btn" onclick={cancelEditTz} disabled={savingTz}>Cancel</button>
        </div>
      </div>
    {/if}
    {#if saveTzOk}
      <div class="save-ok">Saved — restart to apply</div>
    {/if}
  </div>

  <h2 class="section-title">Networking</h2>
  <div class="grid">
    <div class="card">
      <div class="label">Listen Address</div>
      <div class="value value-sm mono">{config.listen || '—'}</div>
    </div>
    <div class="card">
      <div class="label">TLS</div>
      <div class="value value-sm">{config.tls ? 'Enabled' : 'Disabled'}</div>
    </div>
    <div class="card">
      <div class="label">Rate Limit</div>
      <div class="value value-sm">{config.rate_limit > 0 ? `${config.rate_limit} req/s` : 'Unlimited'}</div>
    </div>
    <div class="card">
      <div class="label">CORS Origins</div>
      <div class="value value-sm mono">{config.cors_origins.length > 0 ? config.cors_origins.join(', ') : 'None'}</div>
    </div>
  </div>

  <h2 class="section-title">WebSocket</h2>
  <div class="grid">
    <div class="card">
      <div class="label">Status</div>
      <div class="value value-sm">{config.websocket_enabled ? 'Enabled' : 'Disabled'}</div>
    </div>
    <div class="card">
      <div class="label">Max Connections</div>
      <div class="value value-sm">{config.websocket_max_connections > 0 ? config.websocket_max_connections : 'Unlimited'}</div>
    </div>
    <div class="card">
      <div class="label">Replay Buffer TTL</div>
      <div class="value value-sm mono">{config.websocket_replay_buffer_ttl || '—'}</div>
    </div>
  </div>

  <h2 class="section-title">MCP Server</h2>
  <div class="config-card">
    <div class="config-row">
      <div class="config-label">
        <div class="config-name">Status</div>
        <div class="config-desc">
          Expose an MCP server endpoint at /api/v1/mcp for external MCP clients
          (Claude Code, other AI tools). Authenticate with API keys.
        </div>
      </div>
      <div class="config-value-row">
        <span class="status-dot" class:status-on={config.mcp_server_enabled} class:status-off={!config.mcp_server_enabled}></span>
        <span class="config-value">{config.mcp_server_enabled ? 'Enabled' : 'Disabled'}</span>
        <label class="switch">
          <input type="checkbox" checked={config.mcp_server_enabled} onchange={toggleMcp} disabled={savingMcp} />
          <span class="slider"></span>
        </label>
      </div>
    </div>
  </div>

  {#if config.mcp_server_enabled}
    <div class="config-card" style="margin-top: 14px;">
      <div class="config-row">
        <div class="config-label">
          <div class="config-name">Endpoint</div>
          <div class="config-desc">
            Use this URL when configuring MCP clients. Requires a Bearer token (API key).
          </div>
        </div>
        <div class="config-value-row">
          <span class="config-value mono">{config.mcp_server_endpoint}</span>
          <button class="btn btn-sm" onclick={copyEndpoint}>
            {mcpCopied ? 'Copied' : 'Copy'}
          </button>
        </div>
      </div>
    </div>

    <div class="grid" style="margin-top: 14px;">
      <div class="card">
        <div class="label">Transport</div>
        <div class="value value-sm">
          <select
            class="input inline-select"
            value={config.mcp_server_transport}
            onchange={(e) => saveMcpField('mcp_server_transport', e.target.value)}
            disabled={savingMcp}
          >
            <option value="streamable">Streamable HTTP</option>
            <option value="sse">SSE (legacy)</option>
          </select>
        </div>
      </div>
      <div class="card">
        <div class="label">Session Timeout</div>
        <div class="value value-sm">
          <input
            type="text"
            class="input inline-input"
            value={config.mcp_server_session_timeout || '30m'}
            disabled={savingMcp}
            onchange={(e) => saveMcpField('mcp_server_session_timeout', e.target.value)}
            placeholder="30m"
          />
        </div>
      </div>
      <div class="card">
        <div class="label">Chat Timeout</div>
        <div class="value value-sm">
          <input
            type="text"
            class="input inline-input"
            value={config.mcp_server_chat_timeout || '2m'}
            disabled={savingMcp}
            onchange={(e) => saveMcpField('mcp_server_chat_timeout', e.target.value)}
            placeholder="2m"
          />
        </div>
      </div>
      <div class="card">
        <div class="label">Stateless</div>
        <div class="value value-sm">
          <select
            class="input inline-select"
            value={config.mcp_server_stateless ? 'true' : 'false'}
            onchange={(e) => saveMcpField('mcp_server_stateless', e.target.value === 'true')}
            disabled={savingMcp}
          >
            <option value="false">No</option>
            <option value="true">Yes</option>
          </select>
        </div>
      </div>
    </div>

    <div class="mcp-hint">
      Transport changes require a server restart to take effect.
    </div>
  {/if}
  {#if saveMcpOk}
    <div class="save-ok">Saved</div>
  {/if}

  <h2 class="section-title">External Access</h2>
  <div class="config-card">
    <div class="config-row">
      <div class="config-label">
        <div class="config-name">External URL</div>
        <div class="config-desc">
          Publicly-reachable base URL for this instance. Used for OAuth callback URLs.
          When empty, defaults to the listen address.
        </div>
      </div>
      {#if !editing}
        <div class="config-value-row">
          <span class="config-value mono">{config.external_url || 'Auto-detect'}</span>
          <button class="btn btn-sm" onclick={startEdit}>Edit</button>
        </div>
      {/if}
    </div>
    {#if editing}
      <div class="config-edit">
        <input
          type="url"
          class="input"
          bind:value={editValue}
          placeholder="https://den.example.com"
        />
        <div class="config-actions">
          <button class="btn btn-primary" onclick={saveExternalURL} disabled={saving}>
            {saving ? 'Saving...' : 'Save'}
          </button>
          <button class="btn" onclick={cancelEdit} disabled={saving}>Cancel</button>
        </div>
      </div>
    {/if}
    {#if saveOk}
      <div class="save-ok">Saved</div>
    {/if}
  </div>

  <h2 class="section-title">Process Control</h2>
  <div class="config-card">
    <div class="config-row">
      <div class="config-label">
        <div class="config-name">Reload Configuration</div>
        <div class="config-desc">
          Re-read the TOML config file from disk and update in-memory settings.
          Some changes (listen address, TLS) still require a full restart.
        </div>
      </div>
      <div class="config-value-row">
        <button class="btn" onclick={reloadConfig} disabled={reloading}>
          {reloading ? 'Reloading...' : 'Reload'}
        </button>
      </div>
    </div>
    {#if reloadOk}
      <div class="save-ok">Config reloaded</div>
    {/if}
  </div>

  <div class="config-card" style="margin-top: 14px;">
    <div class="config-row">
      <div class="config-label">
        <div class="config-name">Restart Process</div>
        <div class="config-desc">
          Send a shutdown signal to the running process.
          Requires a process manager (systemd, Docker, K8s) to restart automatically.
        </div>
      </div>
      <div class="config-value-row">
        {#if !confirmRestart}
          <button class="btn btn-danger" onclick={() => { confirmRestart = true }}>
            Restart
          </button>
        {:else}
          <button class="btn btn-danger" onclick={restartProcess} disabled={restarting}>
            {restarting ? 'Restarting...' : 'Confirm Restart'}
          </button>
          <button class="btn" onclick={() => { confirmRestart = false }} disabled={restarting}>Cancel</button>
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .section-title { font-size: 16px; font-weight: 600; margin: 28px 0 12px; }
  .section-title:first-of-type { margin-top: 0; }
  .loading { color: var(--text-muted); }
  .mono { font-family: monospace; }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 14px;
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 20px 16px;
  }
  .label {
    font-size: 11px;
    color: var(--text-muted);
    margin-bottom: 8px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .value { font-size: 24px; font-weight: 700; }
  .value-sm { font-size: 16px; }

  .config-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 20px;
  }
  .config-row {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 16px;
  }
  .config-label { flex: 1; }
  .config-name { font-weight: 600; font-size: 14px; }
  .config-desc { font-size: 12px; color: var(--text-muted); margin-top: 4px; line-height: 1.4; }

  .config-value-row {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-shrink: 0;
  }
  .config-value {
    font-size: 13px;
    color: var(--text-muted);
  }

  .config-edit {
    margin-top: 12px;
  }
  .input {
    width: 100%;
    padding: 8px 10px;
    font-size: 13px;
    font-family: monospace;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--text);
  }
  .input:focus {
    outline: none;
    border-color: var(--accent);
  }
  .config-actions {
    margin-top: 10px;
    display: flex;
    gap: 8px;
  }

  .btn {
    padding: 6px 14px;
    font-size: 13px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    cursor: pointer;
    transition: border-color 0.2s, color 0.2s;
  }
  .btn:hover { border-color: var(--text-muted); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-sm { padding: 4px 10px; font-size: 12px; }
  .btn-primary {
    background: var(--accent);
    color: #fff;
    border-color: var(--accent);
  }
  .btn-primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); }
  .btn-danger {
    background: var(--danger);
    color: #fff;
    border-color: var(--danger);
  }
  .btn-danger:hover { opacity: 0.9; }

  .save-ok {
    margin-top: 8px;
    font-size: 12px;
    color: var(--success);
    font-weight: 500;
  }

  .tz-filter {
    margin-bottom: 6px;
  }
  .tz-select {
    height: auto;
    font-family: monospace;
  }
  .btn-link {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    font-size: 12px;
    padding: 4px 0;
    margin-top: 4px;
  }
  .btn-link:hover { text-decoration: underline; }

  .status-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .status-on { background: var(--success); }
  .status-off { background: var(--text-muted); }

  .switch {
    position: relative;
    display: inline-block;
    width: 36px;
    height: 20px;
    flex-shrink: 0;
  }
  .switch input { opacity: 0; width: 0; height: 0; }
  .slider {
    position: absolute;
    cursor: pointer;
    inset: 0;
    background: var(--border);
    border-radius: 20px;
    transition: background 0.2s;
  }
  .slider::before {
    content: '';
    position: absolute;
    width: 14px;
    height: 14px;
    left: 3px;
    bottom: 3px;
    background: #fff;
    border-radius: 50%;
    transition: transform 0.2s;
  }
  .switch input:checked + .slider { background: var(--accent); }
  .switch input:checked + .slider::before { transform: translateX(16px); }
  .switch input:disabled + .slider { opacity: 0.5; cursor: not-allowed; }

  .inline-select {
    width: auto;
    padding: 4px 8px;
    font-size: 13px;
    font-family: inherit;
  }

  .inline-input {
    width: 80px;
    padding: 4px 8px;
    font-size: 13px;
  }

  .mcp-hint {
    margin-top: 10px;
    font-size: 12px;
    color: var(--text-muted);
  }
</style>
