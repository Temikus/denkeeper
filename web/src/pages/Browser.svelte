<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'

  let sessions = $state([])
  let profiles = $state([])
  let browserConfig = $state(null)
  let loading = $state(true)
  let error = $state('')

  // Confirmation dialog state.
  let confirmAction = $state(null) // { kind: 'clear'|'delete', agent }
  let acting = $state(false)

  async function loadData() {
    loading = true
    error = ''
    try {
      const [sessionsRes, profilesRes, configRes] = await Promise.all([
        api.browserSessions().catch(() => ({ sessions: [] })),
        api.browserProfiles().catch(() => ({ profiles: [] })),
        api.browserConfig().catch(() => null),
      ])
      sessions = sessionsRes.sessions || []
      profiles = profilesRes.profiles || []
      browserConfig = configRes
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  onMount(loadData)

  function formatBytes(bytes) {
    if (bytes === 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB']
    const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
    const val = bytes / Math.pow(1024, i)
    return `${val < 10 ? val.toFixed(1) : Math.round(val)} ${units[i]}`
  }

  function formatDate(dateStr) {
    if (!dateStr) return '--'
    const d = new Date(dateStr)
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
      + ' ' + d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  }

  async function doAction() {
    if (!confirmAction) return
    acting = true
    error = ''
    try {
      if (confirmAction.kind === 'delete') {
        await api.deleteBrowserProfile(confirmAction.agent)
      }
      // For 'clear' we don't have a dedicated API endpoint — clear is
      // only available via the Config MCP tool. The dashboard supports delete.
      confirmAction = null
      await loadData()
    } catch (e) {
      error = e.message
    } finally {
      acting = false
    }
  }
</script>

<h1>Browser</h1>

{#if error}
  <div class="banner error">{error}</div>
{/if}

<!-- Active Sessions -->
<section class="section">
  <div class="section-header">
    <h2>Active Sessions</h2>
    <button class="btn-sm" onclick={loadData}>Refresh</button>
  </div>

  {#if loading}
    <p class="muted">Loading...</p>
  {:else if sessions.length === 0}
    <p class="muted">No active browser sessions.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Name</th>
          <th>Status</th>
          <th>Tool Count</th>
        </tr>
      </thead>
      <tbody>
        {#each sessions as s}
          <tr>
            <td class="mono">{s.name}</td>
            <td>
              <span class="status {s.status === 'connected' ? 'dot-green' : s.status === 'error' ? 'dot-red' : 'dot-grey'}"></span>
              {s.status}
            </td>
            <td>{s.tool_count}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<!-- Browser Profiles -->
<section class="section">
  <h2>Profiles</h2>

  {#if loading}
    <p class="muted">Loading...</p>
  {:else if profiles.length === 0}
    <p class="muted">No browser profiles yet. Profiles are created automatically when agents use browser automation.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Agent</th>
          <th>Size</th>
          <th>Domains</th>
          <th>Last Used</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each profiles as p}
          <tr>
            <td class="mono">{p.agent}</td>
            <td>{formatBytes(p.size_bytes)}</td>
            <td>
              {#if p.domains && p.domains.length > 0}
                {#each p.domains.slice(0, 5) as d}
                  <span class="pill">{d}</span>
                {/each}
                {#if p.domains.length > 5}
                  <span class="pill muted">+{p.domains.length - 5} more</span>
                {/if}
              {:else}
                <span class="muted">{p.domain_count > 0 ? `${p.domain_count} domains` : 'none'}</span>
              {/if}
            </td>
            <td class="muted">{formatDate(p.last_used)}</td>
            <td>
              <button class="btn-sm danger" onclick={() => { confirmAction = { kind: 'delete', agent: p.agent } }}>
                Delete
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</section>

<!-- Browser Configuration -->
{#if browserConfig}
  <section class="section">
    <h2>Configuration</h2>
    <div class="config-grid">
      <div class="config-item">
        <span class="config-label">Image</span>
        <span class="config-value mono">{browserConfig.image || '--'}</span>
      </div>
      <div class="config-item">
        <span class="config-label">Memory Limit</span>
        <span class="config-value">{browserConfig.memory_limit || '--'}</span>
      </div>
      <div class="config-item">
        <span class="config-label">CPU Limit</span>
        <span class="config-value">{browserConfig.cpu_limit || '--'}</span>
      </div>
      <div class="config-item">
        <span class="config-label">Session TTL</span>
        <span class="config-value">{browserConfig.session_ttl || '--'}</span>
      </div>
      <div class="config-item">
        <span class="config-label">Max Pages</span>
        <span class="config-value">{browserConfig.max_pages || '--'}</span>
      </div>
      <div class="config-item">
        <span class="config-label">URL Allowlist</span>
        <span class="config-value">
          {#if browserConfig.url_allowlist?.domains?.length > 0}
            {#each browserConfig.url_allowlist.domains as d}
              <span class="pill">{d}</span>
            {/each}
          {:else}
            <span class="muted">Unrestricted</span>
          {/if}
        </span>
      </div>
    </div>
  </section>
{/if}

<!-- Delete Confirmation Modal -->
{#if confirmAction}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmAction = null }} role="dialog" aria-modal="true">
    <div class="confirm-modal">
      <h2>Delete Profile</h2>
      <p>
        Permanently delete the browser profile for <strong>{confirmAction.agent}</strong>?
        This removes all cookies, localStorage, and cached data.
      </p>
      <div class="modal-actions">
        <button class="btn-danger" onclick={doAction} disabled={acting}>
          {acting ? 'Deleting...' : 'Delete'}
        </button>
        <button class="btn-ghost" onclick={() => confirmAction = null}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  h1 { font-size: 20px; margin-bottom: 20px; }
  h2 { font-size: 15px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 12px; }

  .section { margin-bottom: 32px; }
  .section-header { display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
  .section-header h2 { margin-bottom: 0; }

  table { width: 100%; border-collapse: collapse; }
  th {
    text-align: left;
    padding: 8px 12px;
    border-bottom: 1px solid var(--border);
    color: var(--text-muted);
    font-weight: 500;
    font-size: 12px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  td {
    padding: 10px 12px;
    border-bottom: 1px solid var(--border);
    vertical-align: middle;
  }

  .mono { font-size: 13px; }

  .status {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    margin-right: 6px;
    vertical-align: middle;
  }
  .dot-green { background: var(--success); }
  .dot-red { background: var(--danger); }
  .dot-grey { background: var(--text-muted); }

  /* Config grid */
  .config-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
    gap: 12px;
  }
  .config-item {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 12px 16px;
  }
  .config-label {
    display: block;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    margin-bottom: 4px;
  }
  .config-value {
    font-size: 14px;
  }
</style>
