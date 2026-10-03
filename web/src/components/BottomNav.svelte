<script>
  import { navigate } from '../router.js'
  import { attention } from '../attention.js'

  let { active = '' } = $props()

  const tabs = [
    { id: 'overview', label: 'Overview' },
    { id: 'chat', label: 'Chat' },
    { id: 'approvals', label: 'Approvals' },
    { id: 'agents', label: 'Agents' },
    { id: 'more', label: 'More' },
  ]

  function go(id) {
    navigate(id)
  }
</script>

<nav class="bottom-nav" aria-label="Main navigation">
  {#each tabs as tab}
    <button
      class="tab"
      class:active={active === tab.id || (tab.id === 'overview' && active === '')}
      onclick={() => go(tab.id)}
      aria-current={active === tab.id ? 'page' : undefined}
    >
      {#if tab.id === 'overview'}
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
          <rect x="3" y="3" width="7" height="7" rx="1" />
          <rect x="14" y="3" width="7" height="7" rx="1" />
          <rect x="3" y="14" width="7" height="7" rx="1" />
          <rect x="14" y="14" width="7" height="7" rx="1" />
        </svg>
      {:else if tab.id === 'chat'}
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
          <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z" />
        </svg>
      {:else if tab.id === 'approvals'}
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
          <path d="M9 11l3 3L22 4" />
          <path d="M21 12v7a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2h11" />
        </svg>
        {#if $attention.pendingApprovals > 0}
          <span class="badge" aria-hidden="true">{$attention.pendingApprovals}</span>
        {/if}
      {:else if tab.id === 'agents'}
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
          <path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2" />
          <circle cx="12" cy="7" r="4" />
        </svg>
      {:else if tab.id === 'more'}
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
          <circle cx="12" cy="12" r="1" />
          <circle cx="19" cy="12" r="1" />
          <circle cx="5" cy="12" r="1" />
        </svg>
      {/if}
      <span class="tab-label">{tab.label}</span>
      {#if tab.id === 'approvals' && $attention.pendingApprovals > 0}
        <span class="sr-only">, {$attention.pendingApprovals} pending</span>
      {/if}
    </button>
  {/each}
</nav>

<style>
  .bottom-nav {
    display: none;
    position: fixed;
    bottom: 0;
    left: 0;
    right: 0;
    justify-content: space-around;
    align-items: center;
    background: var(--surface);
    border-top: 1px solid var(--border);
    padding: 10px 0 calc(10px + var(--safe-area-bottom));
    z-index: 50;
  }

  @media (max-width: 768px) {
    .bottom-nav { display: flex; }
  }

  .tab {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    padding: 4px 12px;
    background: none;
    border: none;
    cursor: pointer;
    color: var(--text-muted);
    font-weight: 500;
    -webkit-tap-highlight-color: transparent;
  }

  .tab.active {
    color: var(--accent);
    font-weight: 600;
  }

  .badge {
    position: absolute;
    top: 0;
    left: calc(50% + 4px);
    min-width: 16px;
    padding: 0 5px;
    border-radius: 7px;
    background: var(--warn-badge);
    color: #fff;
    font-size: 10px;
    font-weight: 700;
    line-height: 14px;
    text-align: center;
  }

  .tab-label {
    font-size: 10px;
    line-height: 12px;
    font-family: inherit;
  }
</style>
