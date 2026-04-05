<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { api } from '../api.js'
  import { chatState, sendMessage, newSession, setAgent, loadSession, initChat, resolveApprovalAction } from '../chatStore.js'
  import { wsStatus, initWS, destroyWS } from '../wsStore.js'

  let agents = $state([])
  let sessions = $state([])
  let input = $state('')
  let messagesEl

  // Pending approvals from all adapters (polled).
  let pendingApprovals = $state([])
  let pollTimer

  async function loadAgents() {
    try {
      const res = await api.agents()
      agents = res || []
      await initChat(agents)
    } catch (e) {
      // non-fatal — default will still work
    }
  }

  async function loadSessions() {
    try {
      const res = await api.sessions()
      sessions = (res || []).sort((a, b) =>
        new Date(b.created_at) - new Date(a.created_at)
      )
    } catch (_) {}
  }

  async function selectSession(e) {
    const id = e.target.value
    if (!id) return
    await loadSession(id, $chatState.agent)
  }

  async function loadPendingApprovals() {
    try {
      const all = await api.approvals('pending')
      pendingApprovals = (all || []).filter(a => a.kind === 'tool_call')
    } catch (_) {}
  }

  async function resolvePending(appr, approve, autoApproveScope) {
    appr._resolving = true
    pendingApprovals = [...pendingApprovals]
    try {
      if (approve) {
        await api.approveApproval(appr.id, autoApproveScope || undefined)
      } else {
        await api.denyApproval(appr.id)
      }
      await loadPendingApprovals()
    } catch (_) {
      appr._resolving = false
      pendingApprovals = [...pendingApprovals]
    }
  }

  async function send() {
    const text = input.trim()
    if (!text || $chatState.sending) return
    input = ''
    await sendMessage(text)
  }

  function handleKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  async function resolveApproval(appr, approve, autoApproveScope) {
    appr.resolving = true
    $chatState.messages = [...$chatState.messages]
    try {
      await resolveApprovalAction(appr, approve, autoApproveScope)
      // Status will be updated by tool_start event for approved tools;
      // for denied, update immediately.
      if (!approve) {
        appr.status = 'denied'
        $chatState.messages = [...$chatState.messages]
      }
    } catch (e) {
      appr.resolving = false
      $chatState.messages = [...$chatState.messages]
    }
  }

  function scrollBottom() {
    if (messagesEl) {
      messagesEl.scrollTop = messagesEl.scrollHeight
    }
  }

  // Scroll when messages change.
  $effect(() => {
    $chatState.messages;
    tick().then(scrollBottom)
  })

  // Poll interval: 30s when WS connected, 5s otherwise.
  function startPoll() {
    clearInterval(pollTimer)
    const interval = $wsStatus === 'connected' ? 30000 : 5000
    pollTimer = setInterval(loadPendingApprovals, interval)
  }

  // Restart poll when WS status changes.
  $effect(() => {
    $wsStatus; // track dependency
    startPoll()
  })

  onMount(() => {
    initWS()
    loadAgents()
    loadSessions()
    loadPendingApprovals()
    startPoll()
  })
  onDestroy(() => {
    clearInterval(pollTimer)
    destroyWS()
  })
</script>

<div class="chat-shell">
  <!-- Toolbar -->
  <div class="toolbar">
    <label>
      Agent
      <select bind:value={$chatState.agent} onchange={(e) => setAgent(e.target.value)}>
        {#each agents as a}
          <option value={a.name}>{a.name}</option>
        {/each}
        {#if agents.length === 0}
          <option value="default">default</option>
        {/if}
      </select>
    </label>
    <label>
      Session
      <select value={$chatState.sessionId} onchange={selectSession} disabled={$chatState.sending || $chatState.restoring}>
        <option value="">New session</option>
        {#each sessions as s}
          <option value={s.id}>{s.id.slice(0, 8)} — {s.message_count} msgs — {new Date(s.created_at).toLocaleDateString()}</option>
        {/each}
      </select>
    </label>
    <button class="btn-ghost" onclick={() => { newSession(); }} disabled={!$chatState.sessionId}>New Session</button>
    <button class="btn-ghost" onclick={loadSessions} title="Refresh session list">Refresh</button>
    <span class="ws-status" class:ws-connected={$wsStatus === 'connected'} class:ws-reconnecting={$wsStatus === 'reconnecting'} class:ws-fallback={$wsStatus === 'sse_fallback'}>
      <span class="ws-dot"></span>
      {#if $wsStatus === 'connected'}WS
      {:else if $wsStatus === 'reconnecting'}Reconnecting
      {:else if $wsStatus === 'sse_fallback'}SSE
      {:else if $wsStatus === 'connecting'}Connecting
      {/if}
    </span>
  </div>

  <!-- Pending approvals banner (polled, cross-adapter) -->
  {#if pendingApprovals.length > 0}
    <div class="pending-banner">
      <span class="pending-label">Pending approvals ({pendingApprovals.length})</span>
      {#each pendingApprovals as appr}
        <div class="approval-card pending">
          <span class="approval-icon">?</span>
          <div class="pending-info">
            <span class="tool-name">{appr.summary}</span>
            <span class="pending-meta">{appr.agent_name} · {appr.adapter_name}:{appr.external_id?.slice(0, 8)}</span>
          </div>
          <div class="approval-actions">
            <button class="btn-appr btn-ok" onclick={() => resolvePending(appr, true)} disabled={appr._resolving}>Approve</button>
            <button class="btn-appr btn-bad" onclick={() => resolvePending(appr, false)} disabled={appr._resolving}>Deny</button>
            <button class="btn-appr btn-auto" onclick={() => resolvePending(appr, true, 'permanent')} disabled={appr._resolving} title="Always approve this tool for this agent">Always</button>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  <!-- Message list -->
  <div class="messages" bind:this={messagesEl}>
    {#if $chatState.restoring}
      <div class="empty">
        <p class="muted">Restoring session…</p>
      </div>
    {:else if $chatState.messages.length === 0}
      <div class="empty">
        <p>Send a message to start a conversation.</p>
      </div>
    {/if}
    {#each $chatState.messages as msg}
      <div class="bubble {msg.role}" class:streaming={msg.streaming}>
        <span class="role-label">{msg.role === 'user' ? 'You' : $chatState.agent}</span>
        {#if msg.approvals?.length > 0}
          <div class="approval-cards">
            {#each msg.approvals as appr}
              <div class="approval-card" class:pending={appr.status === 'pending'} class:auto={appr.status === 'auto_approved'}>
                <span class="approval-icon">{appr.status === 'auto_approved' ? '>' : appr.status === 'approved' ? '>' : appr.status === 'denied' ? '!' : '?'}</span>
                <span class="tool-name">{appr.tool}</span>
                {#if appr.status === 'pending'}
                  <div class="approval-actions">
                    <button class="btn-appr btn-ok" onclick={() => resolveApproval(appr, true)} disabled={appr.resolving}>Approve</button>
                    <button class="btn-appr btn-bad" onclick={() => resolveApproval(appr, false)} disabled={appr.resolving}>Deny</button>
                    <button class="btn-appr btn-auto" onclick={() => resolveApproval(appr, true, 'permanent')} disabled={appr.resolving} title="Always approve this tool for this agent">Always</button>
                  </div>
                {:else}
                  <span class="approval-badge">{appr.status === 'auto_approved' ? 'auto-approved' : appr.status}</span>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
        {#if msg.toolCalls?.length > 0}
          <div class="tool-calls">
            {#each msg.toolCalls as tc}
              <div class="tool-call" class:running={tc.status === 'running'} class:error={tc.status === 'error'}>
                <span class="tool-icon">{tc.status === 'running' ? '...' : tc.status === 'error' ? '!' : '>'}</span>
                <span class="tool-name">{tc.name}</span>
                {#if tc.duration}<span class="tool-dur">{tc.duration}ms</span>{/if}
              </div>
            {/each}
          </div>
        {/if}
        {#if msg.streaming && msg.status && !msg.text}
          <p class="status">{msg.status}</p>
        {/if}
        <p class="text">{msg.text}{#if msg.streaming}<span class="cursor">▋</span>{/if}</p>
        {#if msg.tokens}
          <span class="usage">{msg.tokens.toLocaleString()} tokens · ~${msg.costUSD?.toFixed(4) ?? '0.0000'}</span>
        {/if}
      </div>
    {/each}
  </div>

  <!-- Input area -->
  <div class="input-area">
    {#if $chatState.error}
      <div class="error-bar">{$chatState.error}</div>
    {/if}
    <div class="input-row">
      <textarea
        bind:value={input}
        onkeydown={handleKeydown}
        placeholder="Type a message… (Enter to send, Shift+Enter for newline)"
        rows="3"
        disabled={$chatState.sending}
      ></textarea>
      <button class="btn-send" onclick={send} disabled={$chatState.sending || !input.trim()}>
        {$chatState.sending ? '…' : 'Send'}
      </button>
    </div>
  </div>
</div>

<style>
  .chat-shell {
    display: flex;
    flex-direction: column;
    height: calc(100vh - 56px);
    max-width: 820px;
  }

  .toolbar {
    display: flex;
    align-items: center;
    gap: 16px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0;
    flex-shrink: 0;
  }
  .toolbar label {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--text-muted);
  }
  .toolbar select {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 4px 8px;
    font-size: 13px;
  }
  .toolbar select { min-width: 0; max-width: 260px; text-overflow: ellipsis; }
  .toolbar label { flex-shrink: 0; }
  .toolbar label:nth-child(2) { flex: 1; min-width: 0; }

  .ws-status {
    display: flex;
    align-items: center;
    gap: 5px;
    font-size: 11px;
    color: var(--text-muted);
    margin-left: auto;
    flex-shrink: 0;
  }
  .ws-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text-muted);
  }
  .ws-connected .ws-dot { background: #22c55e; }
  .ws-reconnecting .ws-dot { background: #eab308; animation: pulse 1.5s ease-in-out infinite; }
  .ws-fallback .ws-dot { background: var(--accent); }
  @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.3; } }

  .pending-banner {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 12px;
    margin-top: 8px;
    background: rgba(200, 78, 53, 0.06);
    border: 1px solid var(--accent);
    border-radius: var(--radius);
  }
  .pending-label {
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--accent);
    margin-bottom: 2px;
  }
  .pending-info {
    display: flex;
    flex-direction: column;
    gap: 2px;
    flex: 1;
    min-width: 0;
  }
  .pending-info .tool-name {
    font-size: 12px;
    word-break: break-word;
  }
  .pending-meta {
    font-size: 11px;
    color: var(--text-muted);
  }

  .messages {
    flex: 1;
    overflow-y: auto;
    padding: 20px 0;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }

  .empty {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
  }

  .bubble {
    max-width: 80%;
    padding: 10px 14px;
    border-radius: 10px;
    line-height: 1.5;
  }
  .bubble.user {
    align-self: flex-end;
    background: var(--accent);
    color: #fff;
  }
  .bubble.agent {
    align-self: flex-start;
    background: var(--surface);
    border: 1px solid var(--border);
  }
  .role-label {
    display: block;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    opacity: 0.7;
    margin-bottom: 4px;
  }
  .approval-cards { margin-bottom: 8px; display: flex; flex-direction: column; gap: 4px; }
  .approval-card {
    font-size: 12px;
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 8px;
    border-radius: 4px;
    background: var(--surface);
    border: 1px solid var(--border);
    flex-wrap: wrap;
  }
  .approval-card.pending { border-color: var(--accent); background: rgba(99,102,241,0.06); }
  .approval-card.auto { opacity: 0.7; }
  .approval-icon { font-family: monospace; font-weight: bold; width: 16px; text-align: center; }
  .approval-actions { display: flex; gap: 4px; margin-left: auto; }
  .btn-appr {
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 2px 8px;
    font-size: 11px;
    cursor: pointer;
    background: var(--surface);
    color: var(--text);
  }
  .btn-appr:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-ok { border-color: var(--accent); color: var(--accent); }
  .btn-ok:hover:not(:disabled) { background: var(--accent); color: #fff; }
  .btn-bad { border-color: var(--danger); color: var(--danger); }
  .btn-bad:hover:not(:disabled) { background: var(--danger); color: #fff; }
  .btn-auto { border-color: var(--text-muted); color: var(--text-muted); }
  .btn-auto:hover:not(:disabled) { background: var(--text-muted); color: #fff; }
  .approval-badge {
    margin-left: auto;
    font-size: 11px;
    color: var(--text-muted);
    font-style: italic;
  }

  .tool-calls { margin-bottom: 8px; display: flex; flex-direction: column; gap: 4px; }
  .tool-call {
    font-size: 12px;
    color: var(--text-muted);
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 8px;
    border-radius: 4px;
    background: var(--surface);
    border: 1px solid var(--border);
  }
  .tool-call.running { border-color: var(--accent); }
  .tool-call.error { border-color: var(--danger); color: var(--danger); }
  .tool-icon { font-family: monospace; font-weight: bold; width: 16px; text-align: center; }
  .tool-name { font-family: monospace; }
  .tool-dur { margin-left: auto; opacity: 0.6; }

  .status { color: var(--text-muted); font-style: italic; margin: 0 0 4px; font-size: 13px; }
  .text { white-space: pre-wrap; word-break: break-word; margin: 0; }
  .usage { display: block; margin-top: 6px; font-size: 11px; color: var(--text-muted); }
  .cursor { animation: blink 1s step-end infinite; }
  @keyframes blink { 0%, 100% { opacity: 1; } 50% { opacity: 0; } }

  .input-area {
    border-top: 1px solid var(--border);
    padding-top: 16px;
    flex-shrink: 0;
  }
  .error-bar {
    background: rgba(224,92,110,0.15);
    border: 1px solid var(--danger);
    color: var(--danger);
    padding: 8px 12px;
    border-radius: var(--radius);
    font-size: 13px;
    margin-bottom: 10px;
  }
  .input-row {
    display: flex;
    gap: 10px;
    align-items: flex-end;
  }
  textarea {
    flex: 1;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 10px 12px;
    font-size: 14px;
    resize: none;
    font-family: inherit;
    line-height: 1.5;
  }
  textarea:focus { outline: none; border-color: var(--accent); }
  textarea:disabled { opacity: 0.6; }

  .btn-send {
    background: var(--accent);
    color: #fff;
    border: none;
    padding: 10px 20px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 14px;
    height: 42px;
    white-space: nowrap;
  }
  .btn-send:hover:not(:disabled) { background: var(--accent-hover); }
  .btn-send:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-ghost {
    background: none;
    border: 1px solid var(--border);
    color: var(--text-muted);
    padding: 5px 12px;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 13px;
  }
  .btn-ghost:hover { border-color: var(--text-muted); color: var(--text); }

  .muted { color: var(--text-muted); }
</style>
