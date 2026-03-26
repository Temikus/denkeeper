<script>
  import { isAuthenticated } from './store.js'
  import { currentRoute } from './router.js'
  import Nav from './components/Nav.svelte'
  import Login from './pages/Login.svelte'
  import Overview from './pages/Overview.svelte'
  import Agents from './pages/Agents.svelte'
  import Approvals from './pages/Approvals.svelte'
  import Sessions from './pages/Sessions.svelte'
  import Schedules from './pages/Schedules.svelte'
  import Skills from './pages/Skills.svelte'

  // Top-level route segment only (e.g. 'agents' from 'agents/detail').
  $: route = $currentRoute.split('/')[0]
</script>

{#if !$isAuthenticated}
  <Login />
{:else}
  <div class="shell">
    <Nav active={route} />
    <main class="content">
      {#if route === 'overview' || route === ''}
        <Overview />
      {:else if route === 'agents'}
        <Agents />
      {:else if route === 'approvals'}
        <Approvals />
      {:else if route === 'sessions'}
        <Sessions />
      {:else if route === 'schedules'}
        <Schedules />
      {:else if route === 'skills'}
        <Skills />
      {:else}
        <p style="color: var(--text-muted)">Page not found.</p>
      {/if}
    </main>
  </div>
{/if}

<style>
  :global(*, *::before, *::after) {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
  }

  :global(:root) {
    --bg:          #0f1117;
    --surface:     #1a1d27;
    --border:      #2a2d3a;
    --text:        #e2e4eb;
    --text-muted:  #7a7d8e;
    --accent:      #4f8ef7;
    --accent-hover:#6fa3f9;
    --danger:      #e05c6e;
    --success:     #4caf7d;
    --warn:        #f0a958;
    --radius:      6px;
  }

  :global(body) {
    background: var(--bg);
    color: var(--text);
    font-family: 'Inter', 'Segoe UI', system-ui, sans-serif;
    font-size: 14px;
    line-height: 1.5;
  }

  :global(a) {
    color: var(--accent);
    text-decoration: none;
  }

  :global(a:hover) {
    color: var(--accent-hover);
  }

  .shell {
    display: flex;
    height: 100vh;
    overflow: hidden;
  }

  .content {
    flex: 1;
    overflow-y: auto;
    padding: 28px 32px;
  }
</style>
