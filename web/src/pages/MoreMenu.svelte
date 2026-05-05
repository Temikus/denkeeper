<script>
  import { navigate } from '../router.js'
  import { token, authMode, theme } from '../store.js'
  import { api } from '../api.js'
  import { panicStatus } from '../wsStore.js'

  let error = $state('')

  const sections = [
    {
      label: 'Agents',
      items: [
        { id: 'sessions',  label: 'Sessions' },
        { id: 'channels',  label: 'Channels' },
        { id: 'schedules', label: 'Schedules' },
        { id: 'approvals', label: 'Approvals' },
        { id: 'audit',     label: 'Audit Log' },
      ],
    },
    {
      label: 'Platform',
      items: [
        { id: 'skills',  label: 'Skills' },
        { id: 'browser', label: 'Browser' },
        { id: 'kv',      label: 'KV Store' },
      ],
    },
    {
      label: 'Admin',
      items: [
        { id: 'server',    label: 'Server' },
        { id: 'providers', label: 'Providers' },
        { id: 'costs',     label: 'Costs' },
        { id: 'keys',      label: 'API Keys' },
        { id: 'settings',  label: 'Settings' },
      ],
    },
  ]

  function logout() {
    api.logout().catch(() => {})
    token.clear()
    authMode.set(null)
  }

  async function triggerPanic() {
    if (!confirm('Emergency stop: cancel ALL in-flight requests and pause the scheduler?')) return
    try {
      await api.panic()
    } catch (e) {
      error = 'Panic failed: ' + e.message
    }
  }

  async function triggerResume() {
    try {
      await api.resume()
      error = ''
    } catch (e) {
      error = 'Resume failed: ' + e.message
    }
  }
</script>

<h1 class="page-title">More</h1>

{#each sections as section}
  <div class="section">
    <span class="section-label">{section.label}</span>
    <ul class="section-list">
      {#each section.items as item}
        <li>
          <button class="menu-row" onclick={() => navigate(item.id)}>
            <span>{item.label}</span>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>
          </button>
        </li>
      {/each}
    </ul>
  </div>
{/each}

<div class="footer-actions">
  <button class="menu-row" onclick={() => theme.toggle()}>
    <span>Theme</span>
    <span class="meta">{$theme === 'dark' ? 'Dark' : 'Light'}</span>
  </button>

  {#if $panicStatus.active}
    <button class="menu-row danger" onclick={triggerResume}>
      <span>Resume</span>
      <span class="meta">System paused</span>
    </button>
  {:else}
    <button class="menu-row danger" onclick={triggerPanic}>
      <span>Panic</span>
      <span class="meta">Emergency stop</span>
    </button>
  {/if}

  {#if error}
    <p class="error-msg">{error}</p>
  {/if}

  <button class="menu-row" onclick={logout}>
    <span>Logout</span>
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>
  </button>
</div>

<style>
  .page-title {
    font-size: 24px;
    font-weight: 700;
    margin-bottom: 24px;
  }

  .section {
    margin-bottom: 24px;
  }

  .section-label {
    display: block;
    font-size: 11px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-muted);
    padding: 0 0 8px;
  }

  .section-list {
    list-style: none;
  }

  .menu-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 14px 0;
    background: none;
    border: none;
    border-bottom: 1px solid var(--border);
    color: var(--text);
    font-size: 15px;
    font-family: inherit;
    cursor: pointer;
    text-align: left;
    -webkit-tap-highlight-color: transparent;
  }

  .menu-row:active {
    background: var(--hover-overlay);
  }

  .menu-row svg {
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .menu-row.danger span:first-child {
    color: var(--danger);
  }

  .meta {
    font-size: 13px;
    color: var(--text-muted);
  }

  .footer-actions {
    margin-top: 16px;
    padding-top: 8px;
    border-top: 1px solid var(--border);
  }

  .error-msg {
    font-size: 12px;
    color: var(--danger);
    padding: 8px 0;
  }
</style>
