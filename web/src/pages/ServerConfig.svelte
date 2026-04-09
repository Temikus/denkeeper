<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let config = $state(null)
  let loading = $state(true)
  let error = $state('')

  let editing = $state(false)
  let editValue = $state('')
  let saving = $state(false)
  let saveOk = $state(false)

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

  onMount(fetchConfig)
</script>

<h1 class="page-title">Server</h1>
<ErrorBanner message={error} />

{#if loading && !config}
  <p class="loading">Loading...</p>
{:else if config}
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

  .save-ok {
    margin-top: 8px;
    font-size: 12px;
    color: var(--success);
    font-weight: 500;
  }
</style>
