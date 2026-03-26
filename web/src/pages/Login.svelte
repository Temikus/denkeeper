<script>
  import { token } from '../store.js'

  let keyInput = ''
  let error = ''
  let loading = false

  async function handleLogin() {
    error = ''
    if (!keyInput.trim()) {
      error = 'API key is required.'
      return
    }
    loading = true
    try {
      // Validate by calling an authenticated endpoint (requires admin scope).
      const res = await fetch('/api/v1/agents', {
        headers: { 'Authorization': `Bearer ${keyInput.trim()}` },
      })
      if (res.status === 401) {
        error = 'Invalid API key or insufficient scopes.'
        return
      }
      if (!res.ok) {
        error = `Server error: HTTP ${res.status}`
        return
      }
      token.set(keyInput.trim())
    } catch {
      error = 'Could not reach the Denkeeper API.'
    } finally {
      loading = false
    }
  }

  function handleKeydown(e) {
    if (e.key === 'Enter') handleLogin()
  }
</script>

<div class="login-page">
  <div class="card">
    <h1>Denkeeper</h1>
    <p class="subtitle">Enter your API key to access the dashboard.</p>
    {#if error}
      <p class="error">{error}</p>
    {/if}
    <input
      type="password"
      placeholder="API key"
      bind:value={keyInput}
      on:keydown={handleKeydown}
      autocomplete="current-password"
      disabled={loading}
    />
    <button on:click={handleLogin} disabled={loading}>
      {loading ? 'Signing in…' : 'Sign in'}
    </button>
    <p class="hint">
      The key must have scopes: <code>admin</code>, <code>sessions:read</code>,
      <code>costs:read</code>, <code>skills:read</code>, <code>schedules:read</code>,
      <code>approvals:read</code>, <code>approvals:write</code>.
    </p>
  </div>
</div>

<style>
  .login-page {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    background: var(--bg);
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 40px;
    width: min(380px, 90vw);
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h1 { font-size: 22px; font-weight: 700; color: var(--accent); }
  .subtitle { color: var(--text-muted); font-size: 13px; }
  .error { color: var(--danger); font-size: 13px; }
  input {
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    font-size: 14px;
    outline: none;
  }
  input:focus { border-color: var(--accent); }
  input:disabled { opacity: 0.6; }
  button {
    padding: 10px;
    background: var(--accent);
    border: none;
    border-radius: var(--radius);
    color: #fff;
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
  }
  button:hover:not(:disabled) { background: var(--accent-hover); }
  button:disabled { opacity: 0.6; cursor: default; }
  .hint { font-size: 11px; color: var(--text-muted); line-height: 1.5; }
  code { background: var(--border); padding: 1px 4px; border-radius: 3px; }
</style>
