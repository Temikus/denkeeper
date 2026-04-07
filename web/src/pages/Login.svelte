<script>
  import { onMount } from 'svelte'
  import { token, authMode } from '../store.js'
  import { api } from '../api.js'

  // mode: 'loading' | 'login' | 'setup' | 'reveal'
  let mode = $state('loading')

  // Auth config from server.
  let passwordEnabled = $state(false)
  let oidcEnabled = $state(false)

  // Which login method is active: 'password' | 'apikey'
  let activeMethod = $state('password')

  // Login state
  let keyInput = $state('')
  let passwordInput = $state('')
  let loginError = $state('')
  let loginLoading = $state(false)

  // Setup state
  let setupTab = $state('account') // 'account' | 'apikey'
  let accountSetupAvailable = $state(false)

  // Account setup state
  let pinInput = $state('')
  let accountPassword = $state('')
  let accountPasswordConfirm = $state('')
  let accountError = $state('')
  let accountLoading = $state(false)

  // API key setup state
  let setupName = $state('admin')
  let setupScopes = $state({
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
  })
  let setupError = $state('')
  let setupLoading = $state(false)

  // Reveal state (after successful API key setup)
  let revealedKey = $state('')
  let copied = $state(false)

  // Derived: true when there are multiple login method choices.
  let hasMultipleMethods = $derived(passwordEnabled && true) // password + apikey always available

  // All valid API scopes — must match the canonical list in internal/scope/scope.go.
  // The scope sync test (internal/scope/scope_test.go) will fail if any are missing.
  const ALL_SCOPES = [
    { value: 'admin',            label: 'admin' },
    { value: 'chat',             label: 'chat' },
    { value: 'health',           label: 'health' },
    { value: 'agents:read',      label: 'agents:read' },
    { value: 'agents:write',     label: 'agents:write' },
    { value: 'approvals:read',   label: 'approvals:read' },
    { value: 'approvals:write',  label: 'approvals:write' },
    { value: 'browser:read',     label: 'browser:read' },
    { value: 'browser:write',    label: 'browser:write' },
    { value: 'costs:read',       label: 'costs:read' },
    { value: 'kv:read',          label: 'kv:read' },
    { value: 'kv:write',         label: 'kv:write' },
    { value: 'schedules:read',   label: 'schedules:read' },
    { value: 'schedules:write',  label: 'schedules:write' },
    { value: 'sessions:read',    label: 'sessions:read' },
    { value: 'skills:read',      label: 'skills:read' },
    { value: 'skills:write',     label: 'skills:write' },
    { value: 'tools:read',       label: 'tools:read' },
    { value: 'tools:write',      label: 'tools:write' },
  ]

  onMount(async () => {
    // Fetch auth config and setup status in parallel.
    const [authCfg, setupStatus] = await Promise.all([
      api.authConfig().catch(() => ({ password_enabled: false, oidc_enabled: false })),
      api.setupStatus().catch(() => ({ setup_required: false, account_setup_available: false })),
    ])

    passwordEnabled = authCfg.password_enabled
    oidcEnabled = authCfg.oidc_enabled
    accountSetupAvailable = setupStatus.account_setup_available || false

    if (setupStatus.setup_required) {
      // Default to account tab when PIN-based setup is available.
      setupTab = accountSetupAvailable ? 'account' : 'apikey'
      mode = 'setup'
    } else {
      // Choose the best default login method.
      activeMethod = passwordEnabled ? 'password' : 'apikey'
      mode = 'login'
    }
  })

  function switchMethod(method) {
    loginError = ''
    activeMethod = method
  }

  // --- Password Login ---

  async function handlePasswordLogin() {
    loginError = ''
    if (!passwordInput.trim()) {
      loginError = 'Password is required.'
      return
    }
    loginLoading = true
    try {
      await api.passwordLogin(passwordInput.trim())
      authMode.set('session')
    } catch (e) {
      loginError = e.message || 'Login failed.'
    } finally {
      loginLoading = false
    }
  }

  function handlePasswordKeydown(e) {
    if (e.key === 'Enter') handlePasswordLogin()
  }

  // --- API Key Login ---

  async function handleKeyLogin() {
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

  function handleKeyKeydown(e) {
    if (e.key === 'Enter') handleKeyLogin()
  }

  // --- SSO ---

  function handleSSO() {
    window.location.href = '/auth/oidc/login'
  }

  // --- Account Setup (PIN + password) ---

  async function handleAccountSetup() {
    accountError = ''
    if (!pinInput.trim()) {
      accountError = 'PIN is required. Check your server logs.'
      return
    }
    if (accountPassword.length < 8) {
      accountError = 'Password must be at least 8 characters.'
      return
    }
    if (accountPassword !== accountPasswordConfirm) {
      accountError = 'Passwords do not match.'
      return
    }
    accountLoading = true
    try {
      await api.setupAccount(pinInput.trim(), accountPassword)
      authMode.set('session')
    } catch (e) {
      accountError = e.message || 'Account creation failed.'
    } finally {
      accountLoading = false
    }
  }

  function handleAccountKeydown(e) {
    if (e.key === 'Enter') handleAccountSetup()
  }

  // --- API Key Setup ---

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
    activeMethod = 'apikey'
    mode = 'login'
  }
</script>

<div class="login-page">
  <div class="card">

    {#if mode === 'loading'}
      <h1>Denkeeper</h1>
      <p class="subtitle">Loading...</p>

    {:else if mode === 'login'}
      <h1>Denkeeper</h1>
      <p class="subtitle">Sign in to access the dashboard.</p>

      {#if loginError}
        <p class="error">{loginError}</p>
      {/if}

      {#if oidcEnabled}
        <button class="sso-btn" onclick={handleSSO}>Sign in with SSO</button>
        {#if passwordEnabled || activeMethod === 'apikey'}
          <div class="or-divider"><span>or</span></div>
        {/if}
      {/if}

      <!-- Method switcher: shown when password login is available (always has apikey as alternative) -->
      {#if hasMultipleMethods}
        <div class="method-tabs">
          <button
            class="method-tab" class:active={activeMethod === 'password'}
            onclick={() => switchMethod('password')}
          >Password</button>
          <button
            class="method-tab" class:active={activeMethod === 'apikey'}
            onclick={() => switchMethod('apikey')}
          >API Key</button>
        </div>
      {/if}

      {#if activeMethod === 'password' && passwordEnabled}
        <input
          type="password"
          placeholder="Password"
          bind:value={passwordInput}
          onkeydown={handlePasswordKeydown}
          autocomplete="current-password"
          disabled={loginLoading}
        />
        <button type="submit" onclick={handlePasswordLogin} disabled={loginLoading}>
          {loginLoading ? 'Signing in...' : 'Sign in'}
        </button>
      {:else}
        <input
          type="password"
          placeholder="API key (dk_...)"
          bind:value={keyInput}
          onkeydown={handleKeyKeydown}
          autocomplete="off"
          disabled={loginLoading}
        />
        <button onclick={handleKeyLogin} disabled={loginLoading}>
          {loginLoading ? 'Signing in...' : 'Sign in'}
        </button>
        <p class="hint">
          Requires the <code>admin</code> scope for full access,
          or specific read/write scopes for limited access.
        </p>
      {/if}

    {:else if mode === 'setup'}
      <h1>Welcome to Denkeeper</h1>

      {#if accountSetupAvailable}
        <div class="tab-bar">
          <button
            class="tab" class:active={setupTab === 'account'}
            onclick={() => setupTab = 'account'}
          >Create Account</button>
          <button
            class="tab" class:active={setupTab === 'apikey'}
            onclick={() => setupTab = 'apikey'}
          >Create API Key</button>
        </div>
      {/if}

      {#if setupTab === 'account' && accountSetupAvailable}
        <p class="subtitle">Enter the PIN from your server logs and choose a password.</p>
        {#if accountError}
          <p class="error">{accountError}</p>
        {/if}
        <span class="field-label">Setup PIN</span>
        <input
          type="text"
          placeholder="6-digit PIN from server logs"
          bind:value={pinInput}
          onkeydown={handleAccountKeydown}
          inputmode="numeric"
          maxlength="6"
          autocomplete="off"
          disabled={accountLoading}
        />
        <span class="field-label">Password</span>
        <input
          type="password"
          placeholder="Choose a password (min. 8 characters)"
          bind:value={accountPassword}
          onkeydown={handleAccountKeydown}
          autocomplete="new-password"
          disabled={accountLoading}
        />
        <span class="field-label">Confirm password</span>
        <input
          type="password"
          placeholder="Confirm your password"
          bind:value={accountPasswordConfirm}
          onkeydown={handleAccountKeydown}
          autocomplete="new-password"
          disabled={accountLoading}
        />
        <button onclick={handleAccountSetup} disabled={accountLoading}>
          {accountLoading ? 'Creating account...' : 'Create account'}
        </button>
      {:else}
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
          {setupLoading ? 'Creating...' : 'Create key'}
        </button>
      {/if}

    {:else if mode === 'reveal'}
      <h1>Your API key</h1>
      <p class="subtitle">Copy it now -- it will not be shown again.</p>
      <div class="key-box">
        <code class="key-text">{revealedKey}</code>
        <button class="copy-btn" onclick={copyKey}>{copied ? 'Copied' : 'Copy'}</button>
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
  .tab-bar {
    display: flex;
    gap: 0;
    border-bottom: 1px solid var(--border);
    margin-bottom: 2px;
  }
  .tab {
    flex: 1;
    padding: 8px 12px;
    background: none;
    color: var(--text-muted);
    border: none;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    cursor: pointer;
    font-size: 13px;
    font-weight: 600;
  }
  .tab:hover { color: var(--text); background: none; }
  .tab.active {
    color: var(--accent);
    border-bottom-color: var(--accent);
  }
  .method-tabs {
    display: flex;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }
  .method-tab {
    flex: 1;
    padding: 8px 12px;
    background: none;
    color: var(--text-muted);
    border: none;
    border-radius: 0;
    cursor: pointer;
    font-size: 13px;
    font-weight: 600;
    transition: background 0.15s, color 0.15s;
  }
  .method-tab:hover { color: var(--text); background: none; }
  .method-tab.active {
    background: var(--accent);
    color: #fff;
  }
  .or-divider {
    display: flex;
    align-items: center;
    gap: 12px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .or-divider::before,
  .or-divider::after {
    content: '';
    flex: 1;
    border-top: 1px solid var(--border);
  }
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
  .sso-btn {
    background: var(--success);
  }
  .sso-btn:hover { opacity: 0.85; }
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
