<script>
  let { active = 'overview' } = $props()
  import { untrack } from 'svelte'
  import { navigate } from '../router.js'
  import { token, authMode, theme } from '../store.js'
  import { api } from '../api.js'
  import { attention } from '../attention.js'
  import { topLinks, sections } from '../navItems.js'
  import { setup, showSetupReminder, openWizard } from '../setupStore.js'

  const STORAGE_KEY = 'dk_nav_groups'

  // Groups the user closed. Everything starts open; storage can be missing or
  // throw (private mode, blocked site data), and the nav must still render.
  let closed = $state(readClosed())

  function readClosed() {
    try {
      const v = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]')
      return Array.isArray(v) ? v : []
    } catch {
      return []
    }
  }

  function save(next) {
    closed = next
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)) } catch { /* see readClosed */ }
  }

  function toggle(id) {
    save(closed.includes(id) ? closed.filter(c => c !== id) : [...closed, id])
  }

  // Arriving on a page inside a closed group (from a link or the top bar)
  // opens that group, so the current page is visible in the sidebar.
  $effect(() => {
    const home = sections.find(s => s.items.some(i => i.id === active))
    untrack(() => {
      if (home && closed.includes(home.id)) save(closed.filter(c => c !== home.id))
    })
  })

  function badge(id) {
    return id === 'approvals' ? $attention.pendingApprovals : 0
  }

  // Closing a group must not hide a pending count inside it.
  function groupBadge(section) {
    return section.items.reduce((n, i) => n + badge(i.id), 0)
  }

  function hasProblem(section) {
    return section.id === 'platform' && $attention.unhealthyTools.length > 0
  }

  function logout() {
    api.logout().catch(() => {})
    token.clear()
    authMode.set(null)
  }
</script>

<nav class="nav" aria-label="Main navigation">
  <div class="header">
    <div class="brand">Denkeeper</div>
    <button
      class="theme-toggle"
      onclick={() => theme.toggle()}
      aria-label="Toggle theme"
      title="Toggle theme"
      data-testid="theme-toggle"
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden="true"
        width="20"
        height="20"
        fill="currentColor"
        viewBox="0 0 32 32"
      >
        <clipPath id="theme-toggle__horizon__mask">
          <path d="M0 0h32v29h-32z" />
        </clipPath>
        <path d="M30.7 29.9H1.3c-.7 0-1.3.5-1.3 1.1 0 .6.6 1 1.3 1h29.3c.7 0 1.3-.5 1.3-1.1.1-.5-.5-1-1.2-1z" />
        <g clip-path="url(#theme-toggle__horizon__mask)">
          <!-- Sun with rays: visible in light mode, sets in dark mode -->
          <g
            class="sun"
            transform={$theme === 'light' ? 'translate(0,0)' : 'translate(0,32)'}
            style:transition-delay={$theme === 'light' ? '0.2s' : '0s'}
          >
            <path d="M16 8.8c-3.4 0-6.1 2.8-6.1 6.1s2.7 6.3 6.1 6.3 6.1-2.8 6.1-6.1-2.7-6.3-6.1-6.3zm13.3 11L26 15l3.3-4.8c.3-.5.1-1.1-.5-1.2l-5.7-1-1-5.7c-.1-.6-.8-.8-1.2-.5L16 5.1l-4.8-3.3c-.5-.4-1.2-.1-1.3.4L8.9 8 3.2 9c-.6.1-.8.8-.5 1.2L6 15l-3.3 4.8c-.3.5-.1 1.1.5 1.2l5.7 1 1 5.7c.1.6.8.8 1.2.5L16 25l4.8 3.3c.5.3 1.1.1 1.2-.5l1-5.7 5.7-1c.7-.1.9-.8.6-1.3zM16 22.5A7.6 7.6 0 0 1 8.3 15c0-4.2 3.5-7.5 7.7-7.5s7.7 3.4 7.7 7.5c0 4.2-3.4 7.5-7.7 7.5z" />
          </g>
          <!-- Crescent moon: visible in dark mode, sets in light mode -->
          <g
            class="moon"
            transform={$theme === 'dark' ? 'translate(0,0)' : 'translate(0,32)'}
            style:transition-delay={$theme === 'dark' ? '0.2s' : '0s'}
          >
            <path d="M16 5.1C10.5 5.1 6 9.6 6 15.1s4.5 10 10 10c3.8 0 7.1-2.1 8.8-5.3.3-.5 0-1.1-.6-1.1-.4 0-.8.1-1.2.1-5 0-9-4-9-9 0-1.6.4-3.2 1.2-4.5.3-.5 0-1.1-.6-1.2h-.6z" />
          </g>
        </g>
      </svg>
    </button>
  </div>

  <div class="divider"></div>

  <div class="nav-body">
    <ul class="top-links">
      {#each topLinks as l}
        <li>
          <a
            href={'#/' + l.id}
            class="nav-item"
            class:active={active === l.id}
            aria-current={active === l.id ? 'page' : undefined}
            onclick={(e) => { e.preventDefault(); navigate(l.id) }}
          >
            {l.label}
          </a>
        </li>
      {/each}
    </ul>

    {#each sections as section}
      {@const open = !closed.includes(section.id)}
      <div class="section">
        <button
          class="section-toggle"
          aria-expanded={open}
          aria-controls="nav-{section.id}"
          onclick={() => toggle(section.id)}
        >
          <span class="section-label">
            {section.label}{#if !open}<span class="count">{` · ${section.items.length}`}</span>{/if}
            {#if !open && groupBadge(section) > 0}<span class="badge group-badge" aria-hidden="true">{groupBadge(section)}</span><span class="sr-only">({groupBadge(section)} pending)</span>{/if}
            {#if hasProblem(section)}<span class="problem-dot" title="A tool server is unhealthy"></span><span class="sr-only">(a tool server is unhealthy)</span>{/if}
          </span>
          <svg class="chevron" class:open width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true"><polyline points="9 6 15 12 9 18"/></svg>
        </button>
        <ul class="section-items" id="nav-{section.id}" hidden={!open}>
          {#each section.items as l}
            <li>
              <a
                href={'#/' + l.id}
                class="nav-item"
                class:active={active === l.id}
                aria-current={active === l.id ? 'page' : undefined}
                onclick={(e) => { e.preventDefault(); navigate(l.id) }}
              >
                <span>{l.label}</span>
                {#if badge(l.id) > 0}<span class="badge" aria-hidden="true">{badge(l.id)}</span><span class="sr-only">, {badge(l.id)} pending</span>{/if}
              </a>
            </li>
          {/each}
        </ul>
      </div>
    {/each}
  </div>

  <div class="footer">
    {#if $showSetupReminder}
      <button class="setup-chip" onclick={openWizard} data-testid="nav-setup-chip">
        <span class="setup-chip-row">
          <span class="setup-chip-label">Setup · {$setup.doneCount} of {$setup.total}</span>
          <span class="setup-chip-action">Resume</span>
        </span>
        <span class="setup-chip-bar" aria-hidden="true">
          {#each $setup.steps as s (s.id)}<span class:done={s.done}></span>{/each}
        </span>
      </button>
    {/if}
    <button class="logout" onclick={logout} data-testid="logout-btn">Logout</button>
  </div>
</nav>

<style>
  .nav {
    width: 200px;
    background: var(--sidebar-bg);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    flex-shrink: 0;
    overflow-y: auto;
  }

  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 16px 12px;
  }

  .brand {
    font-weight: 700;
    font-size: 16px;
    color: var(--accent);
  }

  .theme-toggle {
    background: none;
    border: none;
    cursor: pointer;
    padding: 4px;
    color: var(--accent);
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--radius);
    transition: transform 0.2s ease, color 0.4s ease;
  }

  .theme-toggle:hover {
    transform: scale(1.15);
  }

  .theme-toggle:active {
    transform: scale(0.9);
  }

  .sun, .moon {
    transition: transform 0.5s cubic-bezier(0.4, 0, 0.2, 1);
  }

  .divider {
    height: 1px;
    background: var(--sidebar-divider);
    margin: 0 16px 8px;
  }

  .nav-body {
    flex: 1;
    overflow-y: auto;
    padding: 0 8px;
  }

  /* Top-level ungrouped links */
  .top-links {
    list-style: none;
    padding: 0 4px;
    margin-bottom: 4px;
  }

  /* Section groups */
  .section {
    margin-top: 16px;
    padding: 0 4px;
  }

  .section-toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 4px 12px 6px;
    background: none;
    border: none;
    border-radius: var(--radius);
    color: var(--sidebar-section-label);
    font: inherit;
    cursor: pointer;
    text-align: left;
  }
  .section-toggle:hover { color: var(--sidebar-text); }
  .section-toggle:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }

  .section-label {
    white-space: nowrap;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .problem-dot {
    display: inline-block;
    vertical-align: middle;
    margin: -2px 0 0 6px;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--danger);
  }

  .group-badge {
    display: inline-block;
    margin-left: 6px;
    letter-spacing: 0;
    vertical-align: middle;
  }

  .chevron { flex-shrink: 0; transition: transform 0.15s; }
  .chevron.open { transform: rotate(90deg); }

  .section-items {
    list-style: none;
    border-left: 2px solid var(--sidebar-border-accent);
    margin-left: 16px;
    padding-left: 8px;
  }

  /* Nav items (shared between top-links and section items) */
  .nav-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 7px 12px;
    color: var(--sidebar-text);
    text-decoration: none;
    border-radius: var(--radius);
    margin: 1px 0;
    font-size: 13.5px;
    transition: background 0.3s, color 0.3s;
  }

  .nav-item:hover {
    background: var(--sidebar-hover-bg);
  }

  .nav-item.active {
    background: var(--sidebar-active-bg);
    color: var(--accent);
    font-weight: 500;
  }

  .badge {
    min-width: 18px;
    padding: 0 6px;
    border-radius: 8px;
    background: var(--warn-badge);
    color: #fff;
    font-size: 11px;
    font-weight: 700;
    line-height: 16px;
    text-align: center;
  }

  /* Footer */
  .footer {
    padding: 12px 16px;
    border-top: 1px solid var(--sidebar-divider);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .setup-chip {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    cursor: pointer;
    font: inherit;
    text-align: left;
    color: var(--text);
  }
  .setup-chip:hover { border-color: var(--accent); }
  .setup-chip-row { display: flex; justify-content: space-between; gap: 8px; font-size: 12px; font-weight: 600; }
  .setup-chip-action { color: var(--accent); }
  .setup-chip-bar { display: flex; gap: 3px; }
  .setup-chip-bar span { flex: 1; height: 3px; border-radius: 2px; background: var(--border); }
  .setup-chip-bar span.done { background: var(--accent); }

  .logout {
    width: 100%;
    padding: 8px;
    background: none;
    border: 1px solid var(--border);
    color: var(--text-muted);
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 13px;
    transition: color 0.3s, border-color 0.3s;
  }

  .logout:hover {
    color: var(--danger);
    border-color: var(--danger);
  }

  @media (max-width: 768px) {
    .nav { display: none; }
  }
</style>
