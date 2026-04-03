<script>
  import { onMount, tick } from 'svelte'
  import { api } from '../api.js'
  import { get } from 'svelte/store'
  import { token } from '../store.js'

  let agents = []
  let selectedAgent = 'default'
  let sessionId = ''
  let messages = [] // { role: 'user'|'agent', text: string, streaming?: bool }
  let input = ''
  let sending = false
  let error = ''
  let messagesEl
  let restoring = false

  const STORAGE_KEY = 'dk_chat_session'

  function saveSession() {
    if (!sessionId) return
    try {
      sessionStorage.setItem(STORAGE_KEY, JSON.stringify({
        sessionId,
        agent: selectedAgent,
      }))
    } catch (_) { /* quota exceeded — ignore */ }
  }

  async function restoreSession() {
    try {
      const raw = sessionStorage.getItem(STORAGE_KEY)
      if (!raw) return
      const saved = JSON.parse(raw)
      if (!saved.sessionId) return

      restoring = true
      const history = await api.sessionMessages(saved.sessionId)
      if (!history || history.length === 0) return

      sessionId = saved.sessionId
      if (saved.agent) selectedAgent = saved.agent
      messages = history.map(m => ({
        role: m.role === 'assistant' ? 'agent' : 'user',
        text: m.content,
      }))
      await tick()
      scrollBottom()
    } catch (_) {
      // session gone or API error — start fresh
    } finally {
      restoring = false
    }
  }

  async function loadAgents() {
    try {
      const res = await api.agents()
      agents = res || []
      if (agents.length > 0 && !sessionId) {
        selectedAgent = agents[0].name
      }
    } catch (e) {
      // non-fatal — default will still work
    }
  }

  function newSession() {
    sessionId = ''
    messages = []
    error = ''
    sessionStorage.removeItem(STORAGE_KEY)
  }

  async function send() {
    const text = input.trim()
    if (!text || sending) return
    input = ''
    error = ''
    sending = true

    messages = [...messages, { role: 'user', text }]
    const agentMsg = { role: 'agent', text: '', streaming: true }
    messages = [...messages, agentMsg]
    await tick()
    scrollBottom()

    try {
      await api.streamChat(selectedAgent, sessionId, text,
        (chunk) => {
          agentMsg.text += chunk
          messages = messages // trigger reactivity
          scrollBottom()
        },
        (doneSessionId) => {
          sessionId = doneSessionId
          agentMsg.streaming = false
          messages = messages
          saveSession()
        }
      )
    } catch (e) {
      agentMsg.text = '⚠ ' + e.message
      agentMsg.streaming = false
      messages = messages
      error = e.message
    } finally {
      sending = false
      scrollBottom()
    }
  }

  function handleKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  function scrollBottom() {
    if (messagesEl) {
      messagesEl.scrollTop = messagesEl.scrollHeight
    }
  }

  onMount(async () => {
    await restoreSession()
    await loadAgents()
  })
</script>

<div class="chat-shell">
  <!-- Toolbar -->
  <div class="toolbar">
    <label>
      Agent
      <select bind:value={selectedAgent}>
        {#each agents as a}
          <option value={a.name}>{a.name}</option>
        {/each}
        {#if agents.length === 0}
          <option value="default">default</option>
        {/if}
      </select>
    </label>
    <span class="session-label">
      {#if sessionId}
        Session: <code>{sessionId}</code>
      {:else}
        <span class="muted">New session</span>
      {/if}
    </span>
    <button class="btn-ghost" onclick={newSession}>New Session</button>
  </div>

  <!-- Message list -->
  <div class="messages" bind:this={messagesEl}>
    {#if restoring}
      <div class="empty">
        <p class="muted">Restoring session…</p>
      </div>
    {:else if messages.length === 0}
      <div class="empty">
        <p>Send a message to start a conversation.</p>
      </div>
    {/if}
    {#each messages as msg}
      <div class="bubble {msg.role}" class:streaming={msg.streaming}>
        <span class="role-label">{msg.role === 'user' ? 'You' : selectedAgent}</span>
        <p class="text">{msg.text}{#if msg.streaming}<span class="cursor">▋</span>{/if}</p>
      </div>
    {/each}
  </div>

  <!-- Input area -->
  <div class="input-area">
    {#if error}
      <div class="error-bar">{error}</div>
    {/if}
    <div class="input-row">
      <textarea
        bind:value={input}
        onkeydown={handleKeydown}
        placeholder="Type a message… (Enter to send, Shift+Enter for newline)"
        rows="3"
        disabled={sending}
      ></textarea>
      <button class="btn-send" onclick={send} disabled={sending || !input.trim()}>
        {sending ? '…' : 'Send'}
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
  .session-label { font-size: 12px; color: var(--text-muted); flex: 1; }
  .session-label code { font-family: monospace; font-size: 11px; }

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
  .text { white-space: pre-wrap; word-break: break-word; margin: 0; }
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
