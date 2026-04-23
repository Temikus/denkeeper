<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'

  let keys = []
  let loading = true
  let error = ''

  // Create modal state
  let showCreate = false
  let createName = ''
  let createScopes = []
  let creating = false
  let newKeyPlaintext = ''
  let newKeyId = ''

  // Confirm action state
  let confirmAction = null // { type: 'revoke'|'rotate'|'delete', id, name }
  let actionLoading = false
  let rotatedKey = ''

  // All valid API scopes — must match the canonical list in internal/scope/scope.go.
  // The scope sync test (internal/scope/scope_test.go) will fail if any are missing.
  const ALL_SCOPES = [
    'admin', 'audit:read', 'channels:read', 'channels:write', 'chat', 'health',
    'agents:read', 'agents:write',
    'approvals:read', 'approvals:write',
    'browser:read', 'browser:write',
    'costs:read',
    'kv:read', 'kv:write',
    'schedules:read', 'schedules:write',
    'sessions:read', 'sessions:write',
    'skills:read', 'skills:write',
    'tools:read', 'tools:write',
  ]

  // Resource groups for the permissions UI
  const RESOURCE_GROUPS = [
    { name: 'Chat', desc: 'Send messages to agents', levels: ['none', 'full'], scopes: { full: ['chat'] } },
    { name: 'Admin', desc: 'Administrative operations', levels: ['none', 'full'], scopes: { full: ['admin'] } },
    { name: 'Health', desc: 'Health check endpoint', levels: ['none', 'full'], scopes: { full: ['health'] } },
    { name: 'Sessions', desc: 'View and manage conversation history', levels: ['none', 'read', 'readwrite'], scopes: { read: ['sessions:read'], readwrite: ['sessions:read', 'sessions:write'] } },
    { name: 'Costs', desc: 'View usage and cost data', levels: ['none', 'read'], scopes: { read: ['costs:read'] } },
    { name: 'Agents', desc: 'View and manage agents', levels: ['none', 'read', 'readwrite'], scopes: { read: ['agents:read'], readwrite: ['agents:read', 'agents:write'] } },
    { name: 'Skills', desc: 'View and manage agent skills', levels: ['none', 'read', 'readwrite'], scopes: { read: ['skills:read'], readwrite: ['skills:read', 'skills:write'] } },
    { name: 'Schedules', desc: 'View and manage scheduled tasks', levels: ['none', 'read', 'readwrite'], scopes: { read: ['schedules:read'], readwrite: ['schedules:read', 'schedules:write'] } },
    { name: 'Approvals', desc: 'Manage approval workflows', levels: ['none', 'read', 'readwrite'], scopes: { read: ['approvals:read'], readwrite: ['approvals:read', 'approvals:write'] } },
    { name: 'Tools', desc: 'Manage MCP tools and plugins', levels: ['none', 'read', 'readwrite'], scopes: { read: ['tools:read'], readwrite: ['tools:read', 'tools:write'] } },
    { name: 'Browser', desc: 'Manage browser profiles and sessions', levels: ['none', 'read', 'readwrite'], scopes: { read: ['browser:read'], readwrite: ['browser:read', 'browser:write'] } },
    { name: 'KV Store', desc: 'Agent key-value storage', levels: ['none', 'read', 'readwrite'], scopes: { read: ['kv:read'], readwrite: ['kv:read', 'kv:write'] } },
    { name: 'Channels', desc: 'View and manage channels', levels: ['none', 'read', 'readwrite'], scopes: { read: ['channels:read'], readwrite: ['channels:read', 'channels:write'] } },
    { name: 'Audit Log', desc: 'View audit trail', levels: ['none', 'read'], scopes: { read: ['audit:read'] } },
  ]

  // Track permission level per resource group
  let resourceLevels = {}
  function resetResourceLevels() {
    resourceLevels = {}
    RESOURCE_GROUPS.forEach(g => resourceLevels[g.name] = 'none')
  }
  resetResourceLevels()

  function setResourceLevel(groupName, level) {
    resourceLevels[groupName] = level
    resourceLevels = resourceLevels // trigger reactivity
    syncScopesFromLevels()
  }

  function syncScopesFromLevels() {
    const scopes = new Set()
    RESOURCE_GROUPS.forEach(g => {
      const level = resourceLevels[g.name]
      if (level !== 'none' && g.scopes[level]) {
        g.scopes[level].forEach(s => scopes.add(s))
      }
    })
    createScopes = [...scopes]
  }

  function setAllRead() {
    RESOURCE_GROUPS.forEach(g => {
      if (g.levels.includes('read')) resourceLevels[g.name] = 'read'
      else if (g.levels.includes('full')) resourceLevels[g.name] = 'full'
    })
    resourceLevels = resourceLevels
    syncScopesFromLevels()
  }

  function setFullAccess() {
    RESOURCE_GROUPS.forEach(g => {
      if (g.levels.includes('readwrite')) resourceLevels[g.name] = 'readwrite'
      else if (g.levels.includes('read')) resourceLevels[g.name] = 'read'
      else if (g.levels.includes('full')) resourceLevels[g.name] = 'full'
    })
    resourceLevels = resourceLevels
    syncScopesFromLevels()
  }

  function levelLabel(level) {
    if (level === 'none') return 'None'
    if (level === 'read') return 'Read'
    if (level === 'readwrite') return 'Read & Write'
    if (level === 'full') return 'Full'
    return level
  }

  async function loadKeys() {
    loading = true
    error = ''
    try {
      keys = await api.listKeys()
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  async function createKey() {
    if (!createName.trim() || createScopes.length === 0) return
    creating = true
    error = ''
    try {
      const res = await api.createKey(createName.trim(), createScopes)
      newKeyPlaintext = res.key
      newKeyId = res.id
      showCreate = false
      createName = ''
      createScopes = []
      await loadKeys()
    } catch (e) {
      error = e.message
    } finally {
      creating = false
    }
  }

  async function confirmRevoke() {
    if (!confirmAction) return
    actionLoading = true
    error = ''
    try {
      await api.revokeKey(confirmAction.id)
      confirmAction = null
      await loadKeys()
    } catch (e) {
      error = e.message
    } finally {
      actionLoading = false
    }
  }

  async function confirmRotate() {
    if (!confirmAction) return
    actionLoading = true
    error = ''
    try {
      const res = await api.rotateKey(confirmAction.id)
      rotatedKey = res.key
      confirmAction = null
      await loadKeys()
    } catch (e) {
      error = e.message
    } finally {
      actionLoading = false
    }
  }

  async function confirmDelete() {
    if (!confirmAction) return
    actionLoading = true
    error = ''
    try {
      await api.deleteKey(confirmAction.id)
      confirmAction = null
      await loadKeys()
    } catch (e) {
      error = e.message
    } finally {
      actionLoading = false
    }
  }

  function copyToClipboard(text) {
    navigator.clipboard.writeText(text).catch(() => {})
  }

  onMount(loadKeys)
</script>

<div class="page">
  <div class="header">
    <h1>API Keys</h1>
    <button class="btn-primary" onclick={() => { showCreate = true; newKeyPlaintext = ''; rotatedKey = ''; createName = ''; resetResourceLevels(); createScopes = [] }}>
      + Create Key
    </button>
  </div>

  {#if error}
    <div class="banner error">{error}</div>
  {/if}

  <!-- Show-once new key banner -->
  {#if newKeyPlaintext}
    <div class="banner success">
      <strong>New key created — copy it now, it will not be shown again:</strong>
      <div class="key-display">
        <code>{newKeyPlaintext}</code>
        <button class="btn-copy" onclick={() => copyToClipboard(newKeyPlaintext)}>Copy</button>
      </div>
      <button class="dismiss" onclick={() => newKeyPlaintext = ''}>Dismiss</button>
    </div>
  {/if}

  <!-- Show-once rotated key banner -->
  {#if rotatedKey}
    <div class="banner success">
      <strong>Key rotated — copy the new key now, it will not be shown again:</strong>
      <div class="key-display">
        <code>{rotatedKey}</code>
        <button class="btn-copy" onclick={() => copyToClipboard(rotatedKey)}>Copy</button>
      </div>
      <button class="dismiss" onclick={() => rotatedKey = ''}>Dismiss</button>
    </div>
  {/if}

  {#if loading}
    <p class="muted">Loading…</p>
  {:else if keys.length === 0}
    <p class="muted">No API keys. Create one to get started.</p>
  {:else}
    <table>
      <thead>
        <tr>
          <th>Name</th>
          <th>Scopes</th>
          <th>Created</th>
          <th>Last Used</th>
          <th>Status</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each keys as key}
          <tr class:revoked={key.revoked}>
            <td class="mono">{key.name}</td>
            <td>
              {#each key.scopes as scope}
                <span class="pill">{scope}</span>
              {/each}
            </td>
            <td>{new Date(key.created_at).toLocaleDateString()}</td>
            <td>{key.last_used_at ? new Date(key.last_used_at).toLocaleDateString() : '—'}</td>
            <td>
              {#if key.revoked}
                <span class="tag revoked">Revoked</span>
              {:else}
                <span class="tag active">Active</span>
              {/if}
            </td>
            <td>
              <div class="actions">
                {#if !key.revoked}
                  <button class="btn-sm" onclick={() => { confirmAction = { type: 'rotate', id: key.id, name: key.name } }}>
                    Rotate
                  </button>
                  <button class="btn-sm danger" onclick={() => { confirmAction = { type: 'revoke', id: key.id, name: key.name } }}>
                    Revoke
                  </button>
                {:else}
                  <button class="btn-sm danger" onclick={() => { confirmAction = { type: 'delete', id: key.id, name: key.name } }}>
                    Delete
                  </button>
                {/if}
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>

<!-- Create Key Modal -->
{#if showCreate}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) showCreate = false }} role="dialog" aria-modal="true">
    <div class="create-modal">
      <div class="create-header">
        <div class="create-header-icon">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/></svg>
        </div>
        <div>
          <h2>Create API Key</h2>
          <p class="create-subtitle">Generate a new key for API access</p>
        </div>
        <button class="btn-close" onclick={() => showCreate = false}>&times;</button>
      </div>

      <div class="create-body">
        <hr class="divider" />

        <label class="name-label">
          Name
          <input type="text" bind:value={createName} placeholder="e.g. my-client" />
        </label>

        <div class="permissions-section">
          <div class="permissions-header">
            <div>
              <h3>Permissions</h3>
              <p class="permissions-desc">Configure what this API key can access.</p>
            </div>
            <div class="quick-links">
              <button class="btn-link" onclick={setAllRead}>Read All</button>
              <span class="link-sep">|</span>
              <button class="btn-link" onclick={setFullAccess}>Full Access</button>
            </div>
          </div>

          <div class="resource-list">
            {#each RESOURCE_GROUPS as group}
              <div class="resource-row">
                <div class="resource-info">
                  <span class="resource-name">{group.name}</span>
                  <span class="resource-desc">{group.desc}</span>
                </div>
                <div class="segment-control">
                  {#each group.levels as level}
                    <button
                      class="segment-btn"
                      class:active={resourceLevels[group.name] === level}
                      onclick={() => setResourceLevel(group.name, level)}
                    >
                      {levelLabel(level)}
                    </button>
                  {/each}
                </div>
              </div>
            {/each}
          </div>
        </div>

        {#if createScopes.length === 0}
          <div class="banner warning">
            This API key currently has no permissions. It won't be able to access any resources.
          </div>
        {/if}
      </div>

      <div class="create-footer">
        <p class="footer-note">Your API key will only be shown once after creation.</p>
        <div class="footer-actions">
          <button class="btn-ghost" onclick={() => showCreate = false}>Cancel</button>
          <button class="btn-primary btn-generate" onclick={createKey} disabled={creating || !createName.trim() || createScopes.length === 0}>
            {#if creating}
              Creating…
            {:else}
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
              Generate Key
            {/if}
          </button>
        </div>
      </div>
    </div>
  </div>
{/if}

<!-- Confirm Revoke / Rotate Modal -->
{#if confirmAction}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmAction = null }} role="dialog" aria-modal="true">
    <div class="confirm-modal">
      {#if confirmAction.type === 'revoke'}
        <h2>Revoke Key</h2>
        <p>Revoke <strong>{confirmAction.name}</strong>? This cannot be undone — any clients using this key will lose access immediately.</p>
        <div class="modal-actions">
          <button class="btn-danger" onclick={confirmRevoke} disabled={actionLoading}>
            {actionLoading ? 'Revoking…' : 'Revoke'}
          </button>
          <button class="btn-ghost" onclick={() => confirmAction = null}>Cancel</button>
        </div>
      {:else if confirmAction.type === 'delete'}
        <h2>Delete Key</h2>
        <p>Permanently delete <strong>{confirmAction.name}</strong>? This will remove the key record entirely.</p>
        <div class="modal-actions">
          <button class="btn-danger" onclick={confirmDelete} disabled={actionLoading}>
            {actionLoading ? 'Deleting…' : 'Delete'}
          </button>
          <button class="btn-ghost" onclick={() => confirmAction = null}>Cancel</button>
        </div>
      {:else}
        <h2>Rotate Key</h2>
        <p>Rotate <strong>{confirmAction.name}</strong>? The existing key will be revoked and a new key will be issued.</p>
        <div class="modal-actions">
          <button class="btn-primary" onclick={confirmRotate} disabled={actionLoading}>
            {actionLoading ? 'Rotating…' : 'Rotate'}
          </button>
          <button class="btn-ghost" onclick={() => confirmAction = null}>Cancel</button>
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  .page { max-width: 900px; }
  .header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 24px; }
  h1 { font-size: 20px; font-weight: 600; }

  .key-display {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 8px 0;
    background: var(--bg);
    padding: 8px 12px;
    border-radius: var(--radius);
  }
  .key-display code { font-family: monospace; font-size: 13px; word-break: break-all; flex: 1; }
  .btn-copy {
    background: var(--border);
    border: none;
    color: var(--text);
    padding: 4px 10px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 12px;
    white-space: nowrap;
  }
  .btn-copy:hover { background: var(--accent); }
  .dismiss {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 12px;
    text-decoration: underline;
    padding: 0;
  }

  table { width: 100%; border-collapse: collapse; table-layout: fixed; }
  th { text-align: left; padding: 8px 12px; border-bottom: 1px solid var(--border); color: var(--text-muted); font-weight: 500; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
  td { padding: 10px 12px; border-bottom: 1px solid var(--border); vertical-align: middle; }
  tr.revoked td { opacity: 0.5; }
  th:nth-child(1), td:nth-child(1) { width: 14%; } /* Name */
  th:nth-child(2), td:nth-child(2) { width: 34%; } /* Scopes */
  th:nth-child(3), td:nth-child(3) { width: 12%; white-space: nowrap; } /* Created */
  th:nth-child(4), td:nth-child(4) { width: 12%; white-space: nowrap; } /* Last Used */
  th:nth-child(5), td:nth-child(5) { width: 10%; white-space: nowrap; } /* Status */
  th:nth-child(6), td:nth-child(6) { width: 18%; white-space: nowrap; } /* Actions */

  .tag { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 11px; font-weight: 600; }
  .tag.active { background: rgba(76,175,125,0.2); color: var(--success); }
  .tag.revoked { background: rgba(224,92,110,0.15); color: var(--danger); }

  .actions { display: flex; gap: 6px; }

  /* Create modal — uses global .overlay for backdrop */
  .create-modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    width: 560px;
    max-width: 90vw;
    padding: 0;
    max-height: 90vh;
    display: flex;
    flex-direction: column;
  }
  .create-modal h2 { font-size: 16px; font-weight: 600; margin-bottom: 0; }
  .create-modal p { color: var(--text-muted); margin-bottom: 20px; line-height: 1.6; }
  .create-modal label { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; font-size: 13px; color: var(--text-muted); }
  .create-modal input[type="text"] {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 8px 12px;
    font-size: 14px;
  }
  .create-modal input[type="text"]:focus { outline: none; border-color: var(--accent); }
  .create-body {
    overflow-y: auto;
    flex: 1;
    min-height: 0;
  }
  .create-header {
    display: flex;
    align-items: flex-start;
    gap: 14px;
    padding: 24px 28px 0;
    flex-shrink: 0;
  }
  .create-header-icon {
    width: 44px;
    height: 44px;
    border-radius: 12px;
    background: rgba(var(--accent-rgb, 108, 92, 231), 0.12);
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--accent);
    flex-shrink: 0;
  }
  .create-subtitle { color: var(--text-muted); font-size: 13px; margin: 2px 0 0 !important; }
  .btn-close {
    margin-left: auto;
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 22px;
    cursor: pointer;
    padding: 0 4px;
    line-height: 1;
  }
  .btn-close:hover { color: var(--text); }
  .divider { border: none; border-top: 1px solid var(--border); margin: 20px 0 0; }
  .name-label { padding: 16px 28px 0; display: flex; flex-direction: column; gap: 6px; font-size: 13px; color: var(--text-muted); }

  .permissions-section { padding: 20px 28px 0; }
  .permissions-header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 12px;
  }
  .permissions-header h3 { font-size: 14px; font-weight: 600; margin: 0; }
  .permissions-desc { color: var(--text-muted); font-size: 12px; margin: 2px 0 0 !important; }
  .quick-links { display: flex; align-items: center; gap: 8px; }
  .btn-link {
    background: none;
    border: none;
    color: var(--accent);
    font-size: 13px;
    font-weight: 600;
    cursor: pointer;
    padding: 0;
  }
  .btn-link:hover { text-decoration: underline; }
  .link-sep { color: var(--border); font-size: 13px; }

  .resource-list {
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .resource-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
  }
  .resource-row:not(:last-child) { border-bottom: 1px solid var(--border); }
  .resource-info { display: flex; flex-direction: column; gap: 1px; }
  .resource-name { font-size: 13px; font-weight: 600; }
  .resource-desc { font-size: 11px; color: var(--text-muted); }

  .segment-control {
    display: flex;
    background: var(--bg);
    border-radius: var(--radius);
    padding: 2px;
    gap: 2px;
  }
  .segment-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    font-size: 12px;
    padding: 4px 12px;
    border-radius: calc(var(--radius) - 2px);
    cursor: pointer;
    white-space: nowrap;
    transition: background 0.15s, color 0.15s;
  }
  .segment-btn:hover { color: var(--text); }
  .segment-btn.active {
    background: var(--surface);
    color: var(--text);
    box-shadow: 0 1px 3px rgba(0,0,0,0.12);
    font-weight: 600;
  }

  /* Override global banner.warning positioning inside create modal */
  .create-body .banner.warning {
    margin: 16px 28px 0;
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .create-body .banner.warning::before {
    content: '\26A0';
    font-size: 16px;
    flex-shrink: 0;
  }

  .create-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 20px 28px 24px;
    flex-shrink: 0;
    border-top: 1px solid var(--border);
  }
  .footer-note { color: var(--text-muted); font-size: 12px; margin: 0 !important; }
  .footer-actions { display: flex; gap: 10px; align-items: center; }
  .btn-generate {
    display: flex;
    align-items: center;
    gap: 6px;
  }
</style>
