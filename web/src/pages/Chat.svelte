<script>
  import { onMount, onDestroy, tick } from 'svelte'
  import { api } from '../api.js'
  import { chatState, sendMessage, newSession, setAgent, loadSession, initChat, resolveApprovalAction } from '../chatStore.js'
  import { wsStatus, onActivity } from '../wsStore.js'

  let agents = $state([])
  let sessions = $state([])
  let input = $state('')
  let messagesEl
  let textareaEl
  let userAtBottom = $state(true)

  // Pending approvals from all adapters (polled).
  let pendingApprovals = $state([])
  let pollTimer
  let unsubActivity

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
    } catch (e) {
      appr._resolving = false
      pendingApprovals = [...pendingApprovals]
      chatState.update(s => ({ ...s, error: 'Approval failed: ' + e.message }))
    }
  }

  async function send() {
    const text = input.trim()
    if (!text || $chatState.sending) return
    input = ''
    autoResizeTextarea()
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
      chatState.update(s => ({ ...s, error: 'Approval failed: ' + e.message }))
    }
  }

  // Track whether user is scrolled to the bottom.
  function handleScroll() {
    if (!messagesEl) return
    const threshold = 60
    userAtBottom = messagesEl.scrollHeight - messagesEl.scrollTop - messagesEl.clientHeight < threshold
  }

  function scrollBottom() {
    if (messagesEl && userAtBottom) {
      messagesEl.scrollTop = messagesEl.scrollHeight
    }
  }

  // Scroll when messages change — only if user is at bottom.
  $effect(() => {
    $chatState.messages;
    tick().then(scrollBottom)
  })

  // Auto-resize textarea to fit content.
  function autoResizeTextarea() {
    if (!textareaEl) return
    textareaEl.style.height = 'auto'
    textareaEl.style.height = Math.min(textareaEl.scrollHeight, 160) + 'px'
  }

  function handleInput() {
    autoResizeTextarea()
  }

  function copyText(text) {
    navigator.clipboard.writeText(text).catch(() => {})
  }

  function dismissError() {
    chatState.update(s => ({ ...s, error: '' }))
  }

  function formatCost(costUSD) {
    if (costUSD == null) return '$0.00'
    if (costUSD < 0.001) {
      const cents = costUSD * 100
      if (cents < 0.01) return '<$0.01'
      return `$${cents.toFixed(2)}c`
    }
    return `$${costUSD.toFixed(4)}`
  }

  function approvalStatusIcon(status) {
    switch (status) {
      case 'auto_approved': return '\u2713'
      case 'approved': return '\u2713'
      case 'denied': return '\u2717'
      default: return '\u25cb'
    }
  }

  function approvalStatusLabel(status) {
    switch (status) {
      case 'auto_approved': return 'auto-approved'
      case 'approved': return 'approved'
      case 'denied': return 'denied'
      default: return 'pending'
    }
  }

  function toolStatusIcon(status) {
    switch (status) {
      case 'running': return '\u2026'
      case 'error': return '\u2717'
      default: return '\u2713'
    }
  }

  // Restart poll when WS status changes; cleanup previous interval via effect teardown.
  $effect(() => {
    const interval = $wsStatus === 'connected' ? 30000 : 5000
    pollTimer = setInterval(loadPendingApprovals, interval)
    return () => clearInterval(pollTimer)
  })

  onMount(() => {
    loadAgents()
    loadSessions()
    loadPendingApprovals()

    // Refresh session list (and active conversation) when another adapter
    // processes a message, so Telegram/Discord activity appears in real time.
    unsubActivity = onActivity((frame) => {
      loadSessions()
      if ($chatState.sessionId === frame.conversation_id) {
        loadSession(frame.conversation_id, $chatState.agent)
      }
    })
  })
  onDestroy(() => {
    unsubActivity?.()
  })
</script>

<div class="chat-shell">
  <h1 class="page-title">Chat</h1>
  <!-- Toolbar -->
  <div class="toolbar" role="toolbar" aria-label="Chat controls">
    <label>
      Agent
      <select bind:value={$chatState.agent} onchange={(e) => setAgent(e.target.value)} aria-label="Select agent">
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
      <select value={$chatState.sessionId} onchange={selectSession} disabled={$chatState.sending || $chatState.restoring} aria-label="Select session">
        <option value="">New session</option>
        {#each sessions as s}
          <option value={s.id}>{s.adapter && s.adapter !== 'ws' && s.adapter !== 'api' && s.adapter !== 'sched' ? s.adapter : s.id.slice(0, 8)} — {s.message_count} msgs — {new Date(s.created_at).toLocaleDateString()}</option>
        {/each}
      </select>
    </label>
    <button class="btn-ghost" onclick={() => { newSession(); }} disabled={!$chatState.sessionId}>New Session</button>
    <button class="btn-ghost" onclick={loadSessions} title="Refresh session list">Refresh Sessions</button>
    <span
      class="ws-status"
      class:ws-connected={$wsStatus === 'connected'}
      class:ws-reconnecting={$wsStatus === 'reconnecting'}
      class:ws-fallback={$wsStatus === 'sse_fallback'}
      role="status"
      aria-label={'Connection: ' + ($wsStatus === 'connected' ? 'WebSocket connected' : $wsStatus === 'reconnecting' ? 'Reconnecting' : $wsStatus === 'sse_fallback' ? 'SSE fallback' : 'Connecting')}
    >
      <span class="ws-dot" aria-hidden="true"></span>
      {#if $wsStatus === 'connected'}WS
      {:else if $wsStatus === 'reconnecting'}Reconnecting
      {:else if $wsStatus === 'sse_fallback'}SSE
      {:else if $wsStatus === 'connecting'}Connecting
      {/if}
    </span>
  </div>

  <!-- Pending approvals banner (polled, cross-adapter) -->
  {#if pendingApprovals.length > 0}
    <div class="pending-banner" role="alert">
      <span class="pending-label">Pending approvals ({pendingApprovals.length})</span>
      {#each pendingApprovals as appr}
        <div class="approval-card pending">
          <span class="approval-icon" aria-hidden="true">{approvalStatusIcon('pending')}</span>
          <span class="sr-only">pending</span>
          <div class="pending-info">
            <span class="tool-name">{appr.summary}</span>
            <span class="pending-meta">{appr.agent_name} · {appr.adapter_name}:{appr.external_id?.slice(0, 8)}</span>
          </div>
          <div class="approval-actions">
            <button class="btn-appr btn-ok" onclick={() => resolvePending(appr, true)} disabled={appr._resolving} aria-label="Approve tool {appr.summary}">Approve</button>
            <button class="btn-appr btn-bad" onclick={() => resolvePending(appr, false)} disabled={appr._resolving} aria-label="Deny tool {appr.summary}">Deny</button>
            <button class="btn-appr btn-auto" onclick={() => resolvePending(appr, true, 'permanent')} disabled={appr._resolving} title="Permanently auto-approve this tool for this agent" aria-label="Always approve {appr.summary}">Always Approve</button>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  <!-- Message list -->
  <div class="messages" bind:this={messagesEl} onscroll={handleScroll} role="log" aria-label="Chat messages" aria-live="off">
    {#if $chatState.restoring}
      <div class="empty">
        <div class="restoring-indicator">
          <span class="spinner" aria-hidden="true"></span>
          <p class="muted">Restoring session...</p>
        </div>
      </div>
    {:else if $chatState.messages.length === 0}
      <div class="empty">
        <p>Send a message to start a conversation.</p>
      </div>
    {/if}
    {#each $chatState.messages as msg}
      <div class="bubble {msg.role}" class:streaming={msg.streaming} class:incomplete={msg.text?.startsWith('\u26a0')} aria-label="{msg.role === 'user' ? 'Your message' : 'Agent response'}">
        <div class="bubble-header">
          <span class="role-label">{msg.role === 'user' ? 'You' : $chatState.agent}</span>
          <button class="btn-copy" onclick={() => copyText(msg.text)} title="Copy message" aria-label="Copy message to clipboard">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
          </button>
        </div>
        {#if msg.approvals?.length > 0}
          <div class="approval-cards">
            {#each msg.approvals as appr}
              <div class="approval-card" class:pending={appr.status === 'pending'} class:auto={appr.status === 'auto_approved'}>
                <span class="approval-icon" aria-hidden="true">{approvalStatusIcon(appr.status)}</span>
                <span class="sr-only">{approvalStatusLabel(appr.status)}</span>
                <span class="tool-name">{appr.tool}</span>
                {#if appr.status === 'pending'}
                  <div class="approval-actions">
                    <button class="btn-appr btn-ok" onclick={() => resolveApproval(appr, true)} disabled={appr.resolving} aria-label="Approve {appr.tool}">Approve</button>
                    <button class="btn-appr btn-bad" onclick={() => resolveApproval(appr, false)} disabled={appr.resolving} aria-label="Deny {appr.tool}">Deny</button>
                    <button class="btn-appr btn-auto" onclick={() => resolveApproval(appr, true, 'permanent')} disabled={appr.resolving} title="Permanently auto-approve this tool for this agent" aria-label="Always approve {appr.tool}">Always Approve</button>
                  </div>
                {:else}
                  <span class="approval-badge">{approvalStatusLabel(appr.status)}</span>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
        {#if msg.toolCalls?.length > 0}
          <div class="tool-calls">
            {#each msg.toolCalls as tc}
              <div class="tool-call" class:running={tc.status === 'running'} class:error={tc.status === 'error'} title={tc.error || ''}>
                <span class="tool-icon" aria-hidden="true">{toolStatusIcon(tc.status)}</span>
                <span class="sr-only">{tc.status}</span>
                <span class="tool-name">{tc.name}</span>
                {#if tc.status === 'running'}
                  <span class="tool-dur">running</span>
                {:else if tc.duration != null}
                  <span class="tool-dur">{tc.duration}ms</span>
                {/if}
                {#if tc.error}
                  <span class="tool-error">{tc.error}</span>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
        {#if msg.thinking}
          <details class="thinking-section">
            <summary>Thinking{#if msg.streaming && !msg.text}<span class="cursor">&#9647;</span>{/if}</summary>
            <p class="thinking-text">{msg.thinking}</p>
          </details>
        {/if}
        {#if msg.costWarning}
          <div class="cost-warning" role="status">{msg.costWarning}</div>
        {/if}
        {#if msg.streaming && !msg.text}
          <span class="typing-dots" aria-label="Thinking"><span></span><span></span><span></span></span>
        {:else}
          <p class="text">{msg.text}{#if msg.streaming}<span class="typing-dots inline" aria-label="Typing"><span></span><span></span><span></span></span>{/if}</p>
        {/if}
        {#if msg.tokens}
          <span class="usage">{msg.tokens.toLocaleString()} tokens · ~{formatCost(msg.costUSD)}</span>
        {/if}
      </div>
    {/each}
  </div>

  <!-- Input area -->
  <div class="input-area">
    {#if $chatState.error}
      <div class="error-bar" role="alert">
        <span>{$chatState.error}</span>
        <button class="btn-dismiss" onclick={dismissError} aria-label="Dismiss error">&times;</button>
      </div>
    {/if}
    <div class="input-row">
      <textarea
        bind:this={textareaEl}
        bind:value={input}
        onkeydown={handleKeydown}
        oninput={handleInput}
        placeholder="Type a message... (Enter to send, Shift+Enter for newline)"
        rows="1"
        aria-label="Chat message input"
      ></textarea>
      <button class="btn-send" onclick={send} disabled={$chatState.sending || !input.trim()} aria-label={$chatState.sending ? 'Sending message' : 'Send message'}>
        {$chatState.sending ? '...' : 'Send'}
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

  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 12px; }

  .toolbar {
    display: flex;
    align-items: center;
    gap: 16px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0;
    flex-shrink: 0;
    flex-wrap: wrap;
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
    scroll-behavior: smooth;
  }

  .empty {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
  }

  .restoring-indicator {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
  }
  .spinner {
    width: 20px;
    height: 20px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  .bubble {
    max-width: 80%;
    padding: 10px 14px;
    border-radius: 10px;
    line-height: 1.5;
    position: relative;
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
  .bubble.incomplete {
    border-color: var(--danger);
    background: rgba(224, 92, 110, 0.06);
  }
  .bubble-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 4px;
  }
  .role-label {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    opacity: 0.7;
  }
  .btn-copy {
    background: none;
    border: none;
    color: inherit;
    opacity: 0;
    cursor: pointer;
    padding: 2px;
    border-radius: 3px;
    transition: opacity 0.15s;
    line-height: 1;
  }
  .bubble:hover .btn-copy { opacity: 0.5; }
  .btn-copy:hover { opacity: 1 !important; }
  .bubble.user .btn-copy { color: #fff; }

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
    flex-wrap: wrap;
  }
  .tool-call.running { border-color: var(--accent); }
  .tool-call.error { border-color: var(--danger); color: var(--danger); }
  .tool-icon { font-family: monospace; font-weight: bold; width: 16px; text-align: center; }
  .tool-name { font-family: monospace; }
  .tool-dur { margin-left: auto; opacity: 0.6; }
  .tool-error {
    width: 100%;
    font-size: 11px;
    color: var(--danger);
    margin-top: 2px;
    word-break: break-word;
  }

  .thinking-section {
    margin-bottom: 6px;
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 12px;
  }
  .thinking-section summary {
    padding: 4px 8px;
    cursor: pointer;
    color: var(--text-muted);
    font-style: italic;
    user-select: none;
  }
  .thinking-section summary:hover { color: var(--text); }
  .thinking-text {
    padding: 6px 8px;
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    color: var(--text-muted);
    border-top: 1px solid var(--border);
    max-height: 200px;
    overflow-y: auto;
  }

  .cost-warning {
    background: rgba(234, 179, 8, 0.15);
    border: 1px solid var(--text-muted);
    color: var(--text);
    padding: 6px 10px;
    border-radius: var(--radius);
    font-size: 12px;
    margin-bottom: 6px;
  }
  .status { color: var(--text-muted); font-style: italic; margin: 0 0 4px; font-size: 13px; }
  .text { white-space: pre-wrap; word-break: break-word; margin: 0; }
  .usage { display: block; margin-top: 6px; font-size: 11px; color: var(--text-muted); }

  .typing-dots {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 0;
  }
  .typing-dots span {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--text-muted);
    animation: dotBounce 1.4s ease-in-out infinite;
  }
  .typing-dots span:nth-child(2) { animation-delay: 0.16s; }
  .typing-dots span:nth-child(3) { animation-delay: 0.32s; }
  .typing-dots.inline {
    margin-left: 4px;
    vertical-align: middle;
  }
  .typing-dots.inline span {
    width: 4px;
    height: 4px;
  }
  @keyframes dotBounce {
    0%, 80%, 100% { opacity: 0.3; transform: scale(0.8); }
    40% { opacity: 1; transform: scale(1); }
  }

  .input-area {
    border-top: 1px solid var(--border);
    padding-top: 16px;
    flex-shrink: 0;
  }
  .error-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    background: rgba(224,92,110,0.15);
    border: 1px solid var(--danger);
    color: var(--danger);
    padding: 8px 12px;
    border-radius: var(--radius);
    font-size: 13px;
    margin-bottom: 10px;
  }
  .btn-dismiss {
    background: none;
    border: none;
    color: var(--danger);
    cursor: pointer;
    font-size: 18px;
    line-height: 1;
    padding: 0 4px;
    opacity: 0.7;
  }
  .btn-dismiss:hover { opacity: 1; }
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
    min-height: 42px;
    max-height: 160px;
    overflow-y: auto;
  }
  textarea:focus { outline: none; border-color: var(--accent); }

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

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
  .muted { color: var(--text-muted); }

  /* Responsive: small screens */
  @media (max-width: 640px) {
    .chat-shell { max-width: 100%; }
    .toolbar { gap: 8px; }
    .toolbar label { font-size: 12px; }
    .toolbar select { max-width: 140px; font-size: 12px; }
    .btn-ghost { padding: 4px 8px; font-size: 12px; }
    .bubble { max-width: 90%; }
    .approval-actions { margin-left: 0; width: 100%; justify-content: flex-end; }
    .pending-banner { padding: 8px; }
  }
</style>
