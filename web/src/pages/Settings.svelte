<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'

  let authStatus = $state(null)
  let sessions = $state([])
  let loading = $state(true)
  let error = $state('')
  let successMsg = $state('')
  let revoking = $state(null) // session ID being revoked, or 'all'
  let confirmRevoke = $state(null) // session ID or 'all' pending confirmation

  // Sections
  let showAuth = $state(true)
  let showSessions = $state(true)

  async function fetchData() {
    loading = true
    error = ''
    try {
      const [status, sess] = await Promise.all([
        api.authStatus(),
        api.listAuthSessions().catch(() => []),
      ])
      authStatus = status
      sessions = Array.isArray(sess) ? sess : []
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  async function revokeSession(id) {
    revoking = id
    error = ''
    try {
      await api.revokeAuthSession(id)
      sessions = sessions.filter(s => s.id !== id)
      if (authStatus) {
        authStatus.active_session_count = Math.max(0, (authStatus.active_session_count || 1) - 1)
      }
      successMsg = 'Session revoked'
      setTimeout(() => { successMsg = '' }, 3000)
    } catch (e) {
      error = e.message
    } finally {
      revoking = null
      confirmRevoke = null
    }
  }

  async function revokeAll() {
    revoking = 'all'
    error = ''
    try {
      const result = await api.revokeAllAuthSessions()
      sessions = []
      if (authStatus) {
        authStatus.active_session_count = 0
      }
      const count = result?.revoked ?? 0
      successMsg = `Revoked ${count} session${count !== 1 ? 's' : ''}`
      setTimeout(() => { successMsg = '' }, 3000)
    } catch (e) {
      error = e.message
    } finally {
      revoking = null
      confirmRevoke = null
    }
  }

  function formatDate(iso) {
    if (!iso) return '—'
    const d = new Date(iso)
    return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
  }

  function shortAgent(ua) {
    if (!ua) return '—'
    if (ua.length > 60) return ua.slice(0, 57) + '...'
    return ua
  }

  onMount(fetchData)
</script>

<h1 class="page-title">Settings</h1>
<ErrorBanner message={error} />

{#if successMsg}
  <div class="success-banner">{successMsg}</div>
{/if}

{#if loading && !authStatus}
  <p class="loading">Loading...</p>
{:else}
  <!-- Auth Methods Overview -->
  <button class="section-toggle" onclick={() => showAuth = !showAuth}>
    <span class="section-arrow" class:open={showAuth}>&#x25B6;</span>
    <h2 class="section-title">Authentication</h2>
  </button>

  {#if showAuth && authStatus}
    <div class="section-body">
      <div class="auth-pills">
        <span class="auth-pill" class:enabled={authStatus.password_enabled} class:disabled={!authStatus.password_enabled}>
          Password {authStatus.password_enabled ? 'Enabled' : 'Disabled'}
        </span>
        <span class="auth-pill" class:enabled={authStatus.oidc_enabled} class:disabled={!authStatus.oidc_enabled}>
          OIDC {authStatus.oidc_enabled ? 'Enabled' : 'Disabled'}
        </span>
        <span class="auth-pill" class:enabled={authStatus.sessions_trackable} class:disabled={!authStatus.sessions_trackable}>
          Sessions {authStatus.sessions_trackable ? 'Tracked' : 'Not tracked'}
        </span>
      </div>

      {#if authStatus.active_session_count !== undefined}
        <p class="session-count">{authStatus.active_session_count} active session{authStatus.active_session_count !== 1 ? 's' : ''}</p>
      {/if}
    </div>
  {/if}

  <!-- Session Management -->
  <button class="section-toggle" onclick={() => showSessions = !showSessions}>
    <span class="section-arrow" class:open={showSessions}>&#x25B6;</span>
    <h2 class="section-title">Sessions</h2>
  </button>

  {#if showSessions}
    <div class="section-body">
      {#if sessions.length === 0}
        <p class="empty-state">No active sessions to display.</p>
      {:else}
        <div class="table-wrap">
          <table class="session-table">
            <thead>
              <tr>
                <th>User Agent</th>
                <th>IP</th>
                <th>Created</th>
                <th>Last Active</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {#each sessions as sess (sess.id)}
                <tr>
                  <td class="ua-cell" title={sess.user_agent}>{shortAgent(sess.user_agent)}</td>
                  <td class="mono">{sess.ip || '—'}</td>
                  <td>{formatDate(sess.created_at)}</td>
                  <td>{formatDate(sess.last_seen_at)}</td>
                  <td class="action-cell">
                    {#if confirmRevoke === sess.id}
                      <button class="btn btn-danger btn-sm" onclick={() => revokeSession(sess.id)} disabled={revoking === sess.id}>
                        {revoking === sess.id ? 'Revoking...' : 'Confirm'}
                      </button>
                      <button class="btn btn-sm" onclick={() => confirmRevoke = null} disabled={revoking === sess.id}>Cancel</button>
                    {:else}
                      <button class="btn btn-sm" onclick={() => confirmRevoke = sess.id} disabled={revoking != null}>Revoke</button>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        <div class="revoke-all-row">
          {#if confirmRevoke === 'all'}
            <button class="btn btn-danger" onclick={revokeAll} disabled={revoking === 'all'}>
              {revoking === 'all' ? 'Revoking...' : 'Confirm Revoke All'}
            </button>
            <button class="btn" onclick={() => confirmRevoke = null} disabled={revoking === 'all'}>Cancel</button>
          {:else}
            <button class="btn" onclick={() => confirmRevoke = 'all'} disabled={revoking != null}>Revoke All Sessions</button>
          {/if}
        </div>
      {/if}
    </div>
  {/if}
{/if}

<style>
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .loading { color: var(--text-muted); }
  .mono { font-family: monospace; font-size: 12px; }

  .success-banner {
    padding: 8px 14px;
    margin-bottom: 16px;
    font-size: 13px;
    color: var(--success);
    background: color-mix(in srgb, var(--success) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--success) 25%, transparent);
    border-radius: var(--radius);
  }

  .section-toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    background: none;
    border: none;
    cursor: pointer;
    padding: 10px 0;
    width: 100%;
    text-align: left;
  }

  .section-arrow {
    font-size: 10px;
    color: var(--text-muted);
    transition: transform 0.2s;
    display: inline-block;
  }
  .section-arrow.open { transform: rotate(90deg); }

  .section-title {
    font-size: 15px;
    font-weight: 600;
    color: var(--text);
    margin: 0;
  }

  .section-body {
    padding: 0 0 20px 18px;
  }

  /* Auth pills */
  .auth-pills {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 8px;
  }

  .auth-pill {
    font-size: 12px;
    padding: 4px 12px;
    border-radius: 12px;
    font-weight: 500;
  }
  .auth-pill.enabled {
    color: var(--success);
    background: color-mix(in srgb, var(--success) 10%, transparent);
    border: 1px solid color-mix(in srgb, var(--success) 25%, transparent);
  }
  .auth-pill.disabled {
    color: var(--text-muted);
    background: color-mix(in srgb, var(--text-muted) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--text-muted) 15%, transparent);
  }

  .session-count {
    font-size: 13px;
    color: var(--text-muted);
  }

  /* Session table */
  .empty-state {
    color: var(--text-muted);
    font-size: 13px;
  }

  .table-wrap {
    overflow-x: auto;
  }

  .session-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }

  .session-table th {
    text-align: left;
    font-size: 11px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    padding: 6px 10px;
    border-bottom: 1px solid var(--border);
  }

  .session-table td {
    padding: 10px;
    border-bottom: 1px solid var(--border);
    vertical-align: middle;
  }

  .session-table tr:last-child td {
    border-bottom: none;
  }

  .ua-cell {
    max-width: 280px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .action-cell {
    white-space: nowrap;
    text-align: right;
    display: flex;
    gap: 6px;
    justify-content: flex-end;
  }

  .revoke-all-row {
    display: flex;
    gap: 8px;
    margin-top: 12px;
  }

  /* Buttons */
  .btn {
    padding: 6px 14px;
    font-size: 13px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    cursor: pointer;
    transition: border-color 0.2s, color 0.2s;
  }
  .btn:hover { border-color: var(--text-muted); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-sm { padding: 4px 10px; font-size: 12px; }
  .btn-danger {
    color: var(--danger);
    border-color: color-mix(in srgb, var(--danger) 40%, transparent);
  }
  .btn-danger:hover {
    background: color-mix(in srgb, var(--danger) 8%, transparent);
    border-color: var(--danger);
  }
</style>
