<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let sessions = []
  let selected = null
  let messages = []
  let error = ''
  let loadingMsgs = false

  onMount(async () => {
    try {
      sessions = (await api.sessions()) || []
    } catch(e) { error = e.message }
  })

  async function selectSession(s) {
    selected = s
    messages = []
    loadingMsgs = true
    error = ''
    try {
      messages = await api.sessionMessages(s.id)
    } catch(e) {
      error = e.message
    } finally {
      loadingMsgs = false
    }
  }

  async function deleteSession(id) {
    if (!confirm(`Delete session ${id.slice(0,12)}? This cannot be undone.`)) return
    try {
      await api.deleteSession(id)
      sessions = sessions.filter(s => s.id !== id)
      if (selected?.id === id) { selected = null; messages = [] }
    } catch(e) { error = e.message }
  }

  function fmtDate(s) {
    if (!s) return '—'
    return new Date(s).toLocaleString()
  }
</script>

<h1 class="page-title">Sessions</h1>
<ErrorBanner message={error} />

<div class="layout">
  <aside class="list">
    {#each sessions as s}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        class="item"
        class:active={selected?.id === s.id}
        onclick={() => selectSession(s)}
        role="button"
        tabindex="0"
      >
        <div class="sid">{s.id.slice(0, 14)}</div>
        <div class="smeta">{fmtDate(s.updated_at || s.created_at)}</div>
        <button class="del" onclick={(e) => { e.stopPropagation(); deleteSession(s.id) }} title="Delete session">✕</button>
      </div>
    {/each}
    {#if sessions.length === 0 && !error}
      <p class="empty">No sessions.</p>
    {/if}
  </aside>

  <section class="thread">
    {#if loadingMsgs}
      <p class="loading">Loading messages…</p>
    {:else if selected}
      {#each messages as m}
        <div class="msg" class:user={m.role === 'user'} class:assistant={m.role === 'assistant'}>
          <div class="role">{m.role}{m.tokens_used ? ` · ${m.tokens_used} tokens` : ''}</div>
          <div class="body">{m.content}</div>
          <div class="ts">{fmtDate(m.created_at)}</div>
        </div>
      {/each}
      {#if messages.length === 0}
        <p class="empty">No messages in this session.</p>
      {/if}
    {:else}
      <p class="empty">Select a session to view messages.</p>
    {/if}
  </section>
</div>

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .layout { display: flex; gap: 16px; height: calc(100vh - 110px); }
  .list {
    width: 240px; flex-shrink: 0;
    overflow-y: auto;
    display: flex; flex-direction: column; gap: 4px;
  }
  .item {
    position: relative;
    padding: 10px 34px 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    cursor: pointer;
  }
  .item:hover, .item.active { border-color: var(--accent); }
  .sid { font-family: monospace; font-size: 12px; }
  .smeta { font-size: 11px; color: var(--text-muted); margin-top: 3px; }
  .del {
    position: absolute; top: 8px; right: 8px;
    background: none; border: none; color: var(--text-muted);
    cursor: pointer; font-size: 13px; padding: 2px 5px;
    border-radius: 3px;
  }
  .del:hover { color: var(--danger); background: rgba(224,92,110,0.1); }
  .thread { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 10px; padding-bottom: 16px; }
  .msg {
    padding: 10px 14px;
    border-radius: var(--radius);
    border: 1px solid var(--border);
    background: var(--surface);
    max-width: 75%;
  }
  .msg.user      { align-self: flex-end; border-color: var(--accent); }
  .msg.assistant { align-self: flex-start; }
  .role { font-size: 10px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 5px; }
  .body { white-space: pre-wrap; word-break: break-word; font-size: 13px; line-height: 1.5; }
  .ts   { font-size: 10px; color: var(--text-muted); margin-top: 6px; }
  .empty, .loading { color: var(--text-muted); font-size: 13px; padding: 8px 0; }
</style>
