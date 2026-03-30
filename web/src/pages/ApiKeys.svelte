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

  const ALL_SCOPES = [
    'chat', 'admin', 'sessions:read', 'costs:read',
    'skills:read', 'schedules:read', 'approvals:read', 'approvals:write',
    'tools:read', 'tools:write',
  ]

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

  function toggleScope(scope) {
    if (createScopes.includes(scope)) {
      createScopes = createScopes.filter(s => s !== scope)
    } else {
      createScopes = [...createScopes, scope]
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
    <button class="btn-primary" onclick={() => { showCreate = true; newKeyPlaintext = ''; rotatedKey = '' }}>
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
            <td class="actions">
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
    <div class="modal">
      <h2>Create API Key</h2>
      <label>
        Name
        <input type="text" bind:value={createName} placeholder="e.g. my-client" />
      </label>
      <fieldset>
        <legend>Scopes</legend>
        <div class="scope-grid">
          {#each ALL_SCOPES as scope}
            <label class="scope-check">
              <input type="checkbox" checked={createScopes.includes(scope)} onchange={() => toggleScope(scope)} />
              {scope}
            </label>
          {/each}
        </div>
      </fieldset>
      <div class="modal-actions">
        <button class="btn-primary" onclick={createKey} disabled={creating || !createName.trim() || createScopes.length === 0}>
          {creating ? 'Creating…' : 'Create'}
        </button>
        <button class="btn-ghost" onclick={() => showCreate = false}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<!-- Confirm Revoke / Rotate Modal -->
{#if confirmAction}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_interactive_supports_focus -->
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmAction = null }} role="dialog" aria-modal="true">
    <div class="modal">
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

  .banner {
    padding: 12px 16px;
    border-radius: var(--radius);
    margin-bottom: 16px;
  }
  .banner.error { background: rgba(224,92,110,0.15); border: 1px solid var(--danger); color: var(--danger); }
  .banner.success { background: rgba(76,175,125,0.12); border: 1px solid var(--success); color: var(--text); }

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

  table { width: 100%; border-collapse: collapse; }
  th { text-align: left; padding: 8px 12px; border-bottom: 1px solid var(--border); color: var(--text-muted); font-weight: 500; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
  td { padding: 10px 12px; border-bottom: 1px solid var(--border); vertical-align: middle; }
  tr.revoked td { opacity: 0.5; }

  .pill { display: inline-block; background: var(--border); color: var(--text-muted); padding: 2px 6px; border-radius: 4px; font-size: 11px; margin: 2px 2px 2px 0; }
  .tag { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 11px; font-weight: 600; }
  .tag.active { background: rgba(76,175,125,0.2); color: var(--success); }
  .tag.revoked { background: rgba(224,92,110,0.15); color: var(--danger); }

  .mono { font-family: monospace; }
  .muted { color: var(--text-muted); }
  .actions { display: flex; gap: 6px; }

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
  .modal h2 { font-size: 16px; font-weight: 600; margin-bottom: 16px; }
  .modal p { color: var(--text-muted); margin-bottom: 20px; line-height: 1.6; }
  .modal label { display: flex; flex-direction: column; gap: 6px; margin-bottom: 16px; font-size: 13px; color: var(--text-muted); }
  .modal input[type="text"] {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 8px 12px;
    font-size: 14px;
  }
  .modal input[type="text"]:focus { outline: none; border-color: var(--accent); }
  fieldset { border: 1px solid var(--border); border-radius: var(--radius); padding: 12px 16px; margin-bottom: 20px; }
  legend { padding: 0 6px; color: var(--text-muted); font-size: 12px; }
  .scope-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
  .scope-check { display: flex; align-items: center; gap: 6px; font-size: 13px; color: var(--text); cursor: pointer; }
  .modal-actions { display: flex; gap: 8px; justify-content: flex-end; }
</style>
