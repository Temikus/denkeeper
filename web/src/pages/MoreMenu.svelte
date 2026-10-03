<script>
  import { navigate } from '../router.js'
  import { token, authMode, theme } from '../store.js'
  import { api } from '../api.js'
  import { attention } from '../attention.js'
  import { sections } from '../navItems.js'

  // Pages that already have a tab in the bottom bar.
  const TABS = new Set(['overview', 'chat', 'approvals', 'agents'])

  const groups = sections
    .map(s => ({ ...s, items: s.items.filter(i => !TABS.has(i.id)) }))
    .filter(s => s.items.length > 0)

  // The first group is short and used daily, so it is always listed; the rest
  // open in place.
  let open = $state({})

  const broken = $derived($attention.unhealthyTools.length)

  function logout() {
    api.logout().catch(() => {})
    token.clear()
    authMode.set(null)
  }
</script>

<h1 class="page-title">More</h1>

{#each groups as group, gi}
  <section class="group" aria-labelledby="more-{group.id}">
    {#if gi === 0}
      <h2 class="group-label" id="more-{group.id}">{group.label}</h2>
      <ul class="card">
        {#each group.items as item}
          <li>
            <button class="menu-row" onclick={() => navigate(item.id)}>
              <span>{item.label}</span>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><polyline points="9 18 15 12 9 6"/></svg>
            </button>
          </li>
        {/each}
      </ul>
    {:else}
      <div class="card">
        <button
          class="group-row"
          aria-expanded={!!open[group.id]}
          aria-controls="more-list-{group.id}"
          onclick={() => { open[group.id] = !open[group.id] }}
        >
          <span class="group-text">
            <span class="group-name" id="more-{group.id}">{group.label}</span>
            <span class="group-summary">{group.items.map(i => i.label).join(', ')}</span>
          </span>
          {#if group.id === 'platform' && broken > 0}
            <span class="problem"><span class="problem-dot" aria-hidden="true"></span>{broken} tool{broken === 1 ? '' : 's'} down</span>
          {/if}
          <svg class="chevron" class:open={open[group.id]} width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><polyline points="9 18 15 12 9 6"/></svg>
        </button>
        <ul class="sub-list" id="more-list-{group.id}" hidden={!open[group.id]}>
          {#each group.items as item}
            <li>
              <button class="menu-row" onclick={() => navigate(item.id)}>
                <span>{item.label}</span>
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><polyline points="9 18 15 12 9 6"/></svg>
              </button>
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  </section>
{/each}

<div class="account">
  <span class="theme-label" id="more-theme">Theme</span>
  <div class="segmented" role="group" aria-labelledby="more-theme">
    <button aria-pressed={$theme !== 'dark'} class:on={$theme !== 'dark'} onclick={() => $theme === 'dark' && theme.toggle()}>Light</button>
    <button aria-pressed={$theme === 'dark'} class:on={$theme === 'dark'} onclick={() => $theme !== 'dark' && theme.toggle()}>Dark</button>
  </div>
  <button class="logout" onclick={logout}>Logout</button>
</div>

<style>
  .page-title { margin-bottom: 16px; }

  .group { margin-bottom: 16px; }

  .group-label {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-muted);
    padding: 0 4px 6px;
  }

  .card {
    list-style: none;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    overflow: hidden;
  }

  .sub-list { list-style: none; border-top: 1px solid var(--border); }

  .menu-row, .group-row {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    padding: 12px 14px;
    background: none;
    border: none;
    color: var(--text);
    font: inherit;
    font-size: 15px;
    cursor: pointer;
    text-align: left;
    -webkit-tap-highlight-color: transparent;
  }
  .menu-row { justify-content: space-between; }
  li + li .menu-row { border-top: 1px solid var(--border); }
  .menu-row:active, .group-row:active { background: var(--hover-overlay); }
  .menu-row:focus-visible, .group-row:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }

  .menu-row svg, .chevron { color: var(--text-muted); flex-shrink: 0; }
  .chevron { transition: transform 0.15s; }
  .chevron.open { transform: rotate(90deg); }

  .group-text { display: flex; flex-direction: column; gap: 1px; flex: 1; min-width: 0; }
  .group-name { font-weight: 600; }
  .group-summary { font-size: 12px; color: var(--text-muted); }

  .problem {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    font-weight: 600;
    color: var(--danger);
    white-space: nowrap;
  }
  .problem-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--danger); }

  .account {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 4px 0;
  }
  .theme-label { flex: 1; font-size: 14px; }

  .segmented {
    display: flex;
    gap: 2px;
    padding: 2px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .segmented button {
    padding: 4px 10px;
    background: none;
    border: none;
    border-radius: 4px;
    color: var(--text-muted);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }
  .segmented button.on { background: var(--bg); color: var(--text); font-weight: 600; }
  .segmented button:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }

  .logout {
    padding: 4px 6px;
    background: none;
    border: none;
    color: var(--danger);
    font: inherit;
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;
  }
</style>
