<script>
  import { onMount } from 'svelte'
  import { token } from '../store.js'
  import { api } from '../api.js'

  // mode: 'loading' | 'login' | 'setup' | 'reveal'
  let mode = 'loading'

  // Login state
  let keyInput = ''
  let loginError = ''
  let loginLoading = false

  // Setup state
  let setupName = 'admin'
  let setupScopes = {
    admin: true,
    chat: true,
    'sessions:read': true,
    'costs:read': true,
    'skills:read': true,
    'schedules:read': true,
    'approvals:read': true,
    'approvals:write': true,
    'tools:read': true,
    'tools:write': true,
  }
  let setupError = ''
  let setupLoading = false

  // Reveal state (after successful setup)
  let revealedKey = ''
  let copied = false

  const ALL_SCOPES = [
    { value: 'admin',           label: 'admin' },
    { value: 'chat',            label: 'chat' },
    { value: 'sessions:read',   label: 'sessions:read' },
    { value: 'costs:read',      label: 'costs:read' },
    { value: 'skills:read',     label: 'skills:read' },
    { value: 'schedules:read',  label: 'schedules:read' },
    { value: 'approvals:read',  label: 'approvals:read' },
    { value: 'approvals:write', label: 'approvals:write' },
    { value: 'tools:read',     label: 'tools:read' },
    { value: 'tools:write',    label: 'tools:write' },
  ]

  onMount(async () => {
    try {
      const status = await api.setupStatus()
      mode = status.setup_required ? 'setup' : 'login'
    } catch {
      // If setup status check fails, fall back to login.
      mode = 'login'
    }
  })

  // --- Login ---

  async function handleLogin() {
    loginError = ''
    if (!keyInput.trim()) {
      loginError = 'API key is required.'
      return
    }
    loginLoading = true
    try {
      const res = await fetch('/api/v1/agents', {
        headers: { 'Authorization': `Bearer ${keyInput.trim()}` },
      })
      if (res.status === 401) {
        loginError = 'Invalid API key or insufficient scopes.'
        return
      }
      if (!res.ok) {
        loginError = `Server error: HTTP ${res.status}`
        return
      }
      token.set(keyInput.trim())
    } catch {
      loginError = 'Could not reach the Denkeeper API.'
    } finally {
      loginLoading = false
    }
  }

  function handleLoginKeydown(e) {
    if (e.key === 'Enter') handleLogin()
  }

  // --- Setup ---

  function selectedScopes() {
    return Object.entries(setupScopes)
      .filter(([, checked]) => checked)
      .map(([s]) => s)
  }

  async function handleSetup() {
    setupError = ''
    const scopes = selectedScopes()
    if (!setupName.trim()) {
      setupError = 'Key name is required.'
      return
    }
    if (scopes.length === 0) {
      setupError = 'Select at least one scope.'
      return
    }
    setupLoading = true
    try {
      const result = await api.setupInit(setupName.trim(), scopes)
      revealedKey = result.key
      mode = 'reveal'
    } catch (e) {
      setupError = e.message || 'Failed to create key.'
    } finally {
      setupLoading = false
    }
  }

  // --- Reveal ---

  async function copyKey() {
    try {
      await navigator.clipboard.writeText(revealedKey)
      copied = true
      setTimeout(() => { copied = false }, 2000)
    } catch {
      // Fallback: select the text in the input
    }
  }

  function proceedToLogin() {
    keyInput = revealedKey
    mode = 'login'
  }
</script>

<div class="login-page">
  <div class="card">

    {#if mode === 'loading'}
      <h1>Denkeeper</h1>
      <p class="subtitle">Loading…</p>

    {:else if mode === 'login'}
      <h1>Denkeeper</h1>
      <p class="subtitle">Enter your API key to access the dashboard.</p>
      {#if loginError}
        <p class="error">{loginError}</p>
      {/if}
      <input
        type="password"
        placeholder="API key (dk_...)"
        bind:value={keyInput}
        onkeydown={handleLoginKeydown}
        autocomplete="current-password"
        disabled={loginLoading}
      />
      <button onclick={handleLogin} disabled={loginLoading}>
        {loginLoading ? 'Signing in…' : 'Sign in'}
      </button>
      <p class="hint">
        The key must have scopes: <code>admin</code>, <code>sessions:read</code>,
        <code>costs:read</code>, <code>skills:read</code>, <code>schedules:read</code>,
        <code>approvals:read</code>, <code>approvals:write</code>,
        <code>tools:read</code>, <code>tools:write</code>.
      </p>

    {:else if mode === 'setup'}
      <h1>Welcome to Denkeeper</h1>
      <p class="subtitle">Create your first API key to access the dashboard.</p>
      {#if setupError}
        <p class="error">{setupError}</p>
      {/if}
      <span class="field-label">Key name</span>
      <input
        type="text"
        placeholder="admin"
        bind:value={setupName}
        disabled={setupLoading}
      />
      <span class="field-label">Scopes</span>
      <div class="scopes-grid">
        {#each ALL_SCOPES as { value, label }}
          <label class="scope-item">
            <input type="checkbox" bind:checked={setupScopes[value]} disabled={setupLoading} />
            <code>{label}</code>
          </label>
        {/each}
      </div>
      <button onclick={handleSetup} disabled={setupLoading || selectedScopes().length === 0}>
        {setupLoading ? 'Creating…' : 'Create key'}
      </button>

    {:else if mode === 'reveal'}
      <h1>Your API key</h1>
      <p class="subtitle">Copy it now — it will not be shown again.</p>
      <div class="key-box">
        <code class="key-text">{revealedKey}</code>
        <button class="copy-btn" onclick={copyKey}>{copied ? '✓' : 'Copy'}</button>
      </div>
      <button onclick={proceedToLogin}>Log in with this key</button>
    {/if}

  </div>
</div>

<style>
  .login-page {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100vh;
    background: var(--bg);
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 40px;
    width: min(420px, 90vw);
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h1 { font-size: 22px; font-weight: 700; color: var(--accent); }
  .subtitle { color: var(--text-muted); font-size: 13px; }
  .error { color: var(--danger); font-size: 13px; }
  .field-label { font-size: 12px; color: var(--text-muted); margin-bottom: -8px; display: block; }
  input[type="text"],
  input[type="password"] {
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    font-size: 14px;
    outline: none;
  }
  input[type="text"]:focus,
  input[type="password"]:focus { border-color: var(--accent); }
  input:disabled { opacity: 0.6; }
  .scopes-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 6px;
  }
  .scope-item {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    cursor: pointer;
  }
  .scope-item input[type="checkbox"] { cursor: pointer; }
  button {
    padding: 10px;
    background: var(--accent);
    color: #fff;
    border: none;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 14px;
    font-weight: 600;
  }
  button:hover:not(:disabled) { background: var(--accent-hover); }
  button:disabled { opacity: 0.6; cursor: default; }
  .key-box {
    display: flex;
    align-items: center;
    gap: 8px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 10px 12px;
  }
  .key-text {
    flex: 1;
    font-size: 12px;
    word-break: break-all;
    color: var(--accent);
  }
  .copy-btn {
    padding: 4px 10px;
    font-size: 12px;
    flex-shrink: 0;
  }
  .hint { font-size: 11px; color: var(--text-muted); line-height: 1.5; }
  code { background: var(--border); padding: 1px 4px; border-radius: 3px; }
  .key-text { background: none; padding: 0; }
</style>
