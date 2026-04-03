<script>
  let { active = 'overview' } = $props()
  import { navigate } from '../router.js'
  import { token } from '../store.js'

  const links = [
    { id: 'overview',  label: 'Overview' },
    { id: 'chat',      label: 'Chat' },
    { id: 'agents',    label: 'Agents' },
    { id: 'approvals', label: 'Approvals' },
    { id: 'sessions',  label: 'Sessions' },
    { id: 'schedules', label: 'Schedules' },
    { id: 'skills',    label: 'Skills' },
    { id: 'tools',     label: 'Tools' },
    { id: 'browser',   label: 'Browser' },
    { id: 'kv',        label: 'KV Store' },
    { id: 'costs',     label: 'Costs' },
    { id: 'keys',      label: 'API Keys' },
  ]

  function logout() {
    token.clear()
  }
</script>

<nav class="nav">
  <div class="brand">Denkeeper</div>
  <ul>
    {#each links as l}
      <li class:active={active === l.id}>
        <a href={'#/' + l.id} onclick={(e) => { e.preventDefault(); navigate(l.id) }}>
          {l.label}
        </a>
      </li>
    {/each}
  </ul>
  <button class="logout" onclick={logout}>Logout</button>
</nav>

<style>
  .nav {
    width: 200px;
    background: var(--surface);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    padding: 16px 0;
    flex-shrink: 0;
  }
  .brand {
    font-weight: 700;
    font-size: 16px;
    padding: 0 16px 16px;
    border-bottom: 1px solid var(--border);
    margin-bottom: 8px;
    color: var(--accent);
  }
  ul { list-style: none; flex: 1; }
  li a {
    display: block;
    padding: 8px 16px;
    color: var(--text-muted);
    text-decoration: none;
    border-radius: var(--radius);
    margin: 2px 8px;
  }
  li a:hover { color: var(--text); background: var(--border); }
  li.active a { color: var(--text); background: var(--border); }
  .logout {
    margin: 8px 16px 0;
    padding: 8px;
    background: none;
    border: 1px solid var(--border);
    color: var(--text-muted);
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 13px;
  }
  .logout:hover { color: var(--danger); border-color: var(--danger); }
</style>
