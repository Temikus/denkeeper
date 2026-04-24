<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let channels = $state([])
  let selected = $state(null)
  let error = $state('')

  let activateKey = $state('')
  let activating = $state(false)
  let deactivating = $state(null)
  let activateError = $state('')
  let confirmDeactivate = $state(null)

  async function loadChannels() {
    try {
      channels = (await api.channels()) || []
      if (selected) selected = channels.find(ch => ch.name === selected.name) || null
    } catch (e) { error = e.message }
  }

  onMount(() => loadChannels())

  async function activateAdapter() {
    const key = activateKey.trim()
    if (!key) { activateError = 'Adapter key is required'; return }
    if (!key.includes(':')) { activateError = 'Format: adapter:externalID (e.g. telegram:387956986)'; return }
    activating = true
    activateError = ''
    try {
      await api.activateChannel(selected.name, key)
      activateKey = ''
      await loadChannels()
    } catch (e) { activateError = e.message }
    finally { activating = false }
  }

  async function doDeactivate(key) {
    deactivating = key
    try {
      await api.deactivateChannel(selected.name, key)
      confirmDeactivate = null
      await loadChannels()
    } catch (e) { error = e.message }
    finally { deactivating = null }
  }
</script>

<h1 class="page-title">Channels</h1>
<ErrorBanner message={error} />

<div class="layout">
  <aside class="list">
    {#each channels as ch}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        class="item"
        class:active={selected?.name === ch.name}
        onclick={() => selected = ch}
        role="button"
        tabindex="0"
      >
        <div class="cname">{ch.name}</div>
        <div class="cmeta">{ch.agent}{#if ch.implicit} · implicit{/if}</div>
      </div>
    {/each}
    {#if channels.length === 0 && !error}
      <p class="empty">No channels configured. Add <code>[[channels]]</code> to your TOML config.</p>
    {/if}
  </aside>

  <section class="detail">
    {#if selected}
      <div class="detail-header">
        <h2 class="detail-name">{selected.name}</h2>
        {#if selected.implicit}
          <span class="badge badge-implicit">Implicit</span>
        {:else}
          <span class="badge badge-explicit">Explicit</span>
        {/if}
      </div>

      <div class="fields">
        <div class="field">
          <span class="field-label">Agent</span>
          <span class="field-value">{selected.agent}</span>
        </div>
        <div class="field">
          <span class="field-label">Session Mode</span>
          <span class="field-value">{selected.session_mode || 'persistent'}</span>
        </div>
        <div class="field">
          <span class="field-label">Conversation ID</span>
          <span class="field-value mono">{selected.conversation_id}{#if selected.session_mode === 'ephemeral'}<span class="muted"> (generated per interaction)</span>{/if}</span>
        </div>
        <div class="field">
          <span class="field-label">Adapters</span>
          <div class="field-value">
            {#if selected.adapters?.length > 0}
              <div class="pills">
                {#each selected.adapters as adapter}
                  <span class="pill">{adapter}</span>
                {/each}
              </div>
            {:else}
              <span class="muted">None</span>
            {/if}
          </div>
        </div>
        <div class="field">
          <span class="field-label">Active Adapter Keys</span>
          <div class="field-value">
            {#if selected.active_adapter_keys?.length > 0}
              <div class="pills">
                {#each selected.active_adapter_keys as key}
                  {#if !selected.implicit}
                    <span class="pill">
                      {key}
                      <button class="pill-remove" onclick={() => confirmDeactivate = key}
                        disabled={deactivating === key} title="Deactivate {key}"
                        aria-label="Deactivate {key}">
                        {deactivating === key ? '\u2026' : '\u00d7'}
                      </button>
                    </span>
                  {:else}
                    <span class="pill">{key}</span>
                  {/if}
                {/each}
              </div>
            {:else}
              <span class="muted">None</span>
            {/if}
          </div>
        </div>
      </div>

      {#if !selected.implicit}
        <div class="activate-section">
          <h3 class="section-title">Activate Adapter</h3>
          {#if activateError}
            <div class="inline-error" role="alert">{activateError}</div>
          {/if}
          <div class="activate-form">
            <input
              type="text"
              bind:value={activateKey}
              placeholder="adapter:externalID"
              disabled={activating}
              aria-label="Adapter key"
            />
            <button class="btn-primary" onclick={activateAdapter}
              disabled={activating || !activateKey.trim()}>
              {activating ? 'Activating\u2026' : 'Activate'}
            </button>
          </div>
          <span class="hint">Format: adapter:externalID (e.g. telegram:387956986)</span>
        </div>
      {/if}
    {:else}
      <p class="empty">Select a channel to view details.</p>
    {/if}
  </section>
</div>

{#if confirmDeactivate}
  <div class="overlay" onclick={(e) => { if (e.target === e.currentTarget) confirmDeactivate = null }} onkeydown={(e) => { if (e.key === 'Escape') confirmDeactivate = null }} role="dialog" aria-modal="true" tabindex="-1">
    <div class="confirm-modal">
      <h2>Deactivate Adapter</h2>
      <p>Deactivate <strong>{confirmDeactivate}</strong> from channel <strong>{selected?.name}</strong>?</p>
      <div class="modal-actions">
        <button class="btn-danger" onclick={() => doDeactivate(confirmDeactivate)}
          disabled={deactivating}>
          {deactivating ? 'Deactivating\u2026' : 'Deactivate'}
        </button>
        <button class="btn-ghost" onclick={() => confirmDeactivate = null}>Cancel</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .layout { display: flex; gap: 20px; height: calc(100vh - 110px); }
  .list {
    width: 220px; flex-shrink: 0;
    overflow-y: auto;
    display: flex; flex-direction: column; gap: 4px;
  }
  .item {
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    cursor: pointer;
  }
  .item:hover, .item.active { border-color: var(--accent); }
  .cname { font-family: monospace; font-size: 13px; font-weight: 600; }
  .cmeta { font-size: 11px; color: var(--text-muted); margin-top: 3px; }

  .detail { flex: 1; overflow-y: auto; }
  .detail-header {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 20px;
  }
  .detail-name { font-size: 20px; font-weight: 700; margin: 0; }

  .badge {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 4px;
    font-weight: 500;
  }
  .badge-implicit {
    background: var(--hover-overlay);
    color: var(--text-muted);
    border: 1px solid var(--border);
  }
  .badge-explicit {
    background: rgba(76,175,125,0.12);
    color: var(--success);
    border: 1px solid rgba(76,175,125,0.3);
  }

  .fields {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 4px 18px;
  }
  .field {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 12px 0;
    border-bottom: 1px solid var(--border);
  }
  .field:last-child { border-bottom: none; }
  .field-label {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    width: 140px;
    flex-shrink: 0;
    padding-top: 2px;
  }
  .field-value { font-size: 13px; }
  .mono { font-family: monospace; }

  .pills { display: flex; flex-wrap: wrap; gap: 6px; }
  .pill {
    padding: 3px 10px;
    background: var(--hover-overlay);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 12px;
    font-family: monospace;
  }

  .pill-remove {
    background: none; border: none; cursor: pointer;
    color: var(--text-muted); padding: 0 0 0 6px;
    font-size: 14px; line-height: 1;
  }
  .pill-remove:hover { color: var(--danger); }
  .pill-remove:disabled { cursor: default; opacity: 0.5; }

  .activate-section {
    margin-top: 20px;
    padding-top: 16px;
    border-top: 1px solid var(--border);
  }
  .section-title {
    font-size: 11px;
    font-weight: 500;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.3px;
    margin: 0 0 10px;
  }
  .activate-form {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .activate-form input {
    flex: 1;
    padding: 6px 10px;
    font-size: 13px;
    font-family: monospace;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
  }
  .activate-form input:focus { border-color: var(--accent); outline: none; }
  .inline-error { color: var(--danger); font-size: 12px; margin-bottom: 8px; }
  .hint { font-size: 11px; color: var(--text-muted); margin-top: 6px; display: block; }

  .muted { color: var(--text-muted); }
  .empty { color: var(--text-muted); font-size: 13px; padding: 8px 0; }
  code { font-family: monospace; font-size: 12px; background: var(--hover-overlay); padding: 1px 5px; border-radius: 3px; }
</style>
