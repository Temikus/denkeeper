<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let channels = $state([])
  let selected = $state(null)
  let error = $state('')

  onMount(async () => {
    try {
      channels = (await api.channels()) || []
    } catch (e) {
      error = e.message
    }
  })
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
          <span class="field-label">Conversation ID</span>
          <span class="field-value mono">{selected.conversation_id}</span>
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
                  <span class="pill">{key}</span>
                {/each}
              </div>
            {:else}
              <span class="muted">None</span>
            {/if}
          </div>
        </div>
      </div>
    {:else}
      <p class="empty">Select a channel to view details.</p>
    {/if}
  </section>
</div>

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

  .muted { color: var(--text-muted); }
  .empty { color: var(--text-muted); font-size: 13px; padding: 8px 0; }
  code { font-family: monospace; font-size: 12px; background: var(--hover-overlay); padding: 1px 5px; border-radius: 3px; }
</style>
