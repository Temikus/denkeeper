<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let agents = $state([])
  let selectedAgent = $state('')
  let prefix = $state('')
  let entries = $state([])
  let loading = $state(true)
  let error = $state('')
  let expandedKey = $state(null)
  let confirmDelete = $state(null)
  let deleting = $state(false)
  let showSetForm = $state(false)
  let setKey = $state('')
  let setValue = $state('')
  let setTTL = $state('')
  let saving = $state(false)

  onMount(async () => {
    try {
      const agentList = (await api.agents()) || []
      agents = agentList
      if (agentList.length > 0) {
        selectedAgent = agentList[0].name
        await loadEntries()
      }
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  })

  async function loadEntries() {
    loading = true
    error = ''
    try {
      const data = await api.kvList(selectedAgent, prefix)
      entries = (data && data.entries) || []
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  async function onAgentChange() {
    expandedKey = null
    confirmDelete = null
    await loadEntries()
  }

  async function applyFilter() {
    expandedKey = null
    await loadEntries()
  }

  function toggleExpand(key) {
    expandedKey = expandedKey === key ? null : key
  }

  async function doDelete() {
    if (!confirmDelete) return
    deleting = true
    try {
      await api.kvDelete(selectedAgent, confirmDelete)
      confirmDelete = null
      if (expandedKey === confirmDelete) expandedKey = null
      await loadEntries()
    } catch (e) {
      error = e.message
    } finally {
      deleting = false
    }
  }

  async function doSet() {
    if (!setKey.trim()) return
    saving = true
    error = ''
    try {
      await api.kvSet(selectedAgent, setKey.trim(), setValue, setTTL.trim() || undefined)
      setKey = ''
      setValue = ''
      setTTL = ''
      showSetForm = false
      await loadEntries()
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }

  function truncate(s, n) {
    return s.length > n ? s.slice(0, n) + '\u2026' : s
  }

  function formatExpiry(expiresAt) {
    if (!expiresAt) return '\u2014'
    const d = new Date(expiresAt)
    const now = new Date()
    const diff = d - now
    if (diff <= 0) return 'expired'
    if (diff < 3600000) return Math.ceil(diff / 60000) + 'm'
    if (diff < 86400000) return Math.ceil(diff / 3600000) + 'h'
    return Math.ceil(diff / 86400000) + 'd'
  }

  function formatDate(ts) {
    if (!ts) return '\u2014'
    return new Date(ts).toLocaleString()
  }
</script>

<div class="page-header">
  <h1 class="page-title">KV Store</h1>
  <button class="btn-primary" onclick={() => showSetForm = !showSetForm} aria-expanded={showSetForm} disabled={!selectedAgent}>
    {showSetForm ? 'Cancel' : 'Set Key'}
  </button>
</div>

<ErrorBanner message={error} />

<div class="controls">
  <label class="control-label">
    Agent
    <select bind:value={selectedAgent} onchange={onAgentChange} disabled={loading}>
      {#each agents as a}
        <option value={a.name}>{a.name}</option>
      {/each}
    </select>
  </label>
  <label class="control-label">
    Prefix filter
    <div class="filter-row">
      <input type="text" bind:value={prefix} placeholder="e.g. cache:" onkeydown={(e) => e.key === 'Enter' && applyFilter()} />
      <button class="btn-ghost" onclick={applyFilter} disabled={loading}>Filter</button>
    </div>
  </label>
</div>

<div class="inline-panel" class:open={showSetForm}>
  <div class="inline-panel-inner">
    <form class="inline-form" onsubmit={(e) => { e.preventDefault(); doSet() }}>
      <h2 class="form-title">Set Key</h2>
      <div class="row">
        <label>
          Key
          <input type="text" bind:value={setKey} placeholder="my-key" />
        </label>
        <label>
          TTL
          <input type="text" bind:value={setTTL} placeholder="5m, 24h (optional)" />
        </label>
      </div>
      <label>
        Value
        <textarea bind:value={setValue} placeholder="Value (max 64KB)"></textarea>
      </label>
      <div class="form-actions">
        <button type="button" class="btn-ghost" onclick={() => showSetForm = false}>Cancel</button>
        <button type="submit" class="btn-primary" disabled={saving || !setKey.trim()}>
          {saving ? 'Saving\u2026' : 'Save'}
        </button>
      </div>
    </form>
  </div>
</div>

{#if loading}
  <p class="muted">Loading…</p>
{:else if entries.length === 0}
  <p class="muted">No keys stored{prefix ? ` matching prefix "${prefix}"` : ''} for this agent.</p>
{:else}
  <div class="table-wrapper">
    <table>
      <thead>
        <tr>
          <th>Key</th>
          <th>Value</th>
          <th>TTL</th>
          <th>Updated</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each entries as entry}
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <tr class="row row-clickable" class:row-expanded={expandedKey === entry.key} onclick={() => toggleExpand(entry.key)} tabindex="0">
            <td class="mono key-cell">{entry.key}</td>
            <td class="mono val-cell">{truncate(entry.value, 80)}</td>
            <td class="ttl-cell">{formatExpiry(entry.expires_at)}</td>
            <td class="date-cell">{formatDate(entry.updated_at)}</td>
            <td class="action-cell">
              <button class="btn-sm btn-danger" onclick={(e) => { e.stopPropagation(); confirmDelete = entry.key }}>Delete</button>
            </td>
          </tr>
          {#if expandedKey === entry.key}
            <tr class="expanded-row">
              <td colspan="5">
                <div class="expanded-content">
                  <div class="expanded-label">Full value</div>
                  <pre class="expanded-value">{entry.value}</pre>
                  <div class="expanded-meta">
                    Created: {formatDate(entry.created_at)} &middot; Updated: {formatDate(entry.updated_at)}
                    {#if entry.expires_at} &middot; Expires: {formatDate(entry.expires_at)}{/if}
                  </div>
                </div>
              </td>
            </tr>
          {/if}
        {/each}
      </tbody>
    </table>
  </div>
{/if}

{#if confirmDelete}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="overlay" onclick={() => confirmDelete = null} role="dialog" tabindex="-1">
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="confirm-modal" role="alertdialog" aria-label="Confirm deletion" tabindex="-1" onclick={(e) => e.stopPropagation()}>
      <h2>Delete key?</h2>
      <p>Are you sure you want to delete <code>{confirmDelete}</code> from agent <strong>{selectedAgent}</strong>?</p>
      <div class="modal-actions">
        <button class="btn-ghost" onclick={() => confirmDelete = null} disabled={deleting}>Cancel</button>
        <button class="btn-danger" onclick={doDelete} disabled={deleting}>{deleting ? 'Deleting…' : 'Delete'}</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .controls { display: flex; gap: 16px; margin-bottom: 20px; flex-wrap: wrap; }
  .control-label { font-size: 12px; color: var(--text-muted); display: flex; flex-direction: column; gap: 4px; }
  .control-label select, .control-label input {
    background: var(--bg); border: 1px solid var(--border); color: var(--text);
    padding: 6px 10px; border-radius: var(--radius); font-size: 13px;
  }
  .control-label select:focus, .control-label input:focus { outline: none; border-color: var(--accent); }
  .filter-row { display: flex; gap: 6px; }
  .filter-row input { min-width: 180px; }
  .form-title { font-size: 16px; font-weight: 600; margin-bottom: 16px; }
  .table-wrapper {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius); overflow-x: auto;
  }
  table { width: 100%; border-collapse: collapse; }
  th {
    text-align: left; padding: 10px 14px; font-size: 11px;
    color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em;
    border-bottom: 1px solid var(--border);
  }
  td { padding: 8px 14px; border-bottom: 1px solid var(--border); font-size: 13px; }
  tr:last-child td { border-bottom: none; }
  /* Row expansion: uses shared .row-clickable and .row-expanded from shared.css */
  .key-cell { font-weight: 500; max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .val-cell { max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-muted); }
  .ttl-cell { white-space: nowrap; width: 60px; }
  .date-cell { white-space: nowrap; font-size: 12px; color: var(--text-muted); }
  .action-cell { width: 70px; text-align: right; }
  .expanded-row td { padding: 0; border-bottom: 1px solid var(--border); }
  .expanded-content { padding: 12px 14px; background: var(--hover-overlay); }
  .expanded-label { font-size: 11px; color: var(--text-muted); text-transform: uppercase; margin-bottom: 6px; }
  .expanded-value {
    background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
    padding: 10px 12px; font-size: 12px; font-family: monospace; color: var(--text);
    white-space: pre-wrap; word-break: break-all; max-height: 300px; overflow-y: auto;
  }
  .expanded-meta { font-size: 11px; color: var(--text-muted); margin-top: 8px; }
  .btn-sm.btn-danger { padding: 3px 8px; font-size: 12px; }
  .confirm-modal code { background: var(--bg); padding: 2px 6px; border-radius: 3px; font-size: 12px; }
</style>
