<script>
  import { onMount } from 'svelte'
  import { isAuthenticated, authMode } from './store.js'
  import { currentRoute } from './router.js'
  import { api } from './api.js'
  import Nav from './components/Nav.svelte'
  import Login from './pages/Login.svelte'
  import Overview from './pages/Overview.svelte'
  import Agents from './pages/Agents.svelte'
  import Approvals from './pages/Approvals.svelte'
  import Sessions from './pages/Sessions.svelte'
  import Schedules from './pages/Schedules.svelte'
  import Skills from './pages/Skills.svelte'
  import Chat from './pages/Chat.svelte'
  import Tools from './pages/Tools.svelte'
  import Browser from './pages/Browser.svelte'
  import KV from './pages/KV.svelte'
  import ApiKeys from './pages/ApiKeys.svelte'
  import Costs from './pages/Costs.svelte'
  import './shared.css'

  // Top-level route segment only (e.g. 'agents' from 'agents/detail').
  let route = $derived($currentRoute.split('/')[0])

  // On mount, check if we have a valid session cookie (e.g. after OIDC redirect).
  onMount(async () => {
    try {
      const sess = await api.sessionCheck()
      if (sess.authenticated) {
        authMode.set('session')
      }
    } catch {
      // Session check failed — fall through to login.
    }
  })
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
      {:else if route === 'tools'}
        <Tools />
      {:else if route === 'browser'}
        <Browser />
      {:else if route === 'kv'}
        <KV />
      {:else if route === 'chat'}
        <Chat />
      {:else if route === 'costs'}
        <Costs />
      {:else if route === 'keys'}
        <ApiKeys />
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
    --bg:          #fffbf7;
    --surface:     #faf5eb;
    --border:      #e8dcc8;
    --text:        #3d2a1e;
    --text-muted:  #8a7a6a;
    --accent:      #c84e35;
    --accent-hover:#b5432c;
    --danger:      #c43a3a;
    --success:     #3d8f62;
    --warn:        #c87e30;
    --radius:      6px;
    --hover-overlay: rgba(0, 0, 0, 0.04);
    --overlay-bg:    rgba(0, 0, 0, 0.4);
  }

  :global(:root.dark) {
    --bg:          #1a1210;
    --surface:     #241c18;
    --border:      #3a2e26;
    --text:        #e8ddd4;
    --text-muted:  #9a8a7a;
    --accent:      #e07a5a;
    --accent-hover:#eb9070;
    --danger:      #e05c6e;
    --success:     #4caf7d;
    --warn:        #f0a958;
    --hover-overlay: rgba(255, 255, 255, 0.03);
    --overlay-bg:    rgba(0, 0, 0, 0.6);
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
