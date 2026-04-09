<script>
  let { active = 'overview' } = $props()
  import { navigate } from '../router.js'
  import { token, authMode, theme } from '../store.js'
  import { api } from '../api.js'

  const topLinks = [
    { id: 'overview',  label: 'Overview' },
    { id: 'chat',      label: 'Chat' },
  ]

  const sections = [
    {
      label: 'Agents',
      id: 'section-agents',
      items: [
        { id: 'agents',    label: 'Agents' },
        { id: 'sessions',  label: 'Sessions' },
        { id: 'schedules', label: 'Schedules' },
        { id: 'approvals', label: 'Approvals' },
      ],
    },
    {
      label: 'Platform',
      id: 'section-platform',
      items: [
        { id: 'skills',  label: 'Skills' },
        { id: 'tools',   label: 'Tools' },
        { id: 'browser', label: 'Browser' },
        { id: 'kv',      label: 'KV Store' },
      ],
    },
    {
      label: 'Admin',
      id: 'section-admin',
      items: [
        { id: 'server', label: 'Server' },
        { id: 'costs',  label: 'Costs' },
        { id: 'keys',   label: 'API Keys' },
      ],
    },
  ]

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
            onclick={(e) => { e.preventDefault(); navigate(l.id) }}
          >
            {l.label}
          </a>
        </li>
      {/each}
    </ul>

    {#each sections as section}
      <div class="section" role="group" aria-labelledby={section.id}>
        <span class="section-label" id={section.id}>{section.label}</span>
        <ul class="section-items">
          {#each section.items as l}
            <li>
              <a
                href={'#/' + l.id}
                class="nav-item"
                class:active={active === l.id}
                onclick={(e) => { e.preventDefault(); navigate(l.id) }}
              >
                {l.label}
              </a>
            </li>
          {/each}
        </ul>
      </div>
    {/each}
  </div>

  <div class="footer">
    <button class="logout" onclick={logout}>Logout</button>
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

  .section-label {
    display: block;
    font-size: 11px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--sidebar-section-label);
    padding: 0 12px 6px;
  }

  .section-items {
    list-style: none;
    border-left: 2px solid var(--sidebar-border-accent);
    margin-left: 16px;
    padding-left: 8px;
  }

  /* Nav items (shared between top-links and section items) */
  .nav-item {
    display: block;
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

  /* Footer */
  .footer {
    padding: 12px 16px;
    border-top: 1px solid var(--sidebar-divider);
  }

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
</style>
