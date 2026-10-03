<script>
  import { onMount, tick } from 'svelte'
  import { navigate } from '../router.js'
  import { pendingSkillTest } from '../chatStore.js'
  import { refreshSetup, skipSetup, completeSetup } from '../setupStore.js'
  import { TIERS, TONES, providerMeta, sampleGreeting, toneForTheme } from '../components/wizard/wizardContent.js'
  import WizardRail from '../components/wizard/WizardRail.svelte'
  import PreviewCard from '../components/wizard/PreviewCard.svelte'
  import StepWelcome from '../components/wizard/StepWelcome.svelte'
  import StepProvider from '../components/wizard/StepProvider.svelte'
  import StepAgent from '../components/wizard/StepAgent.svelte'
  import StepPersona from '../components/wizard/StepPersona.svelte'
  import StepChatApp from '../components/wizard/StepChatApp.svelte'
  import StepReady from '../components/wizard/StepReady.svelte'
  import '../components/wizard/wizard.css'

  // Six screens; the rail and "Step N of 4" count the middle four.
  const STEPS = ['welcome', 'provider', 'agent', 'persona', 'chat', 'ready']

  let index = $state(0)
  let loading = $state(true)
  let busy = $state(false)
  let stepRef = $state(null)
  let stepReady = $state(false)
  let heading = $state(null)
  let confirmingLeave = $state(false)
  let leaveError = $state('')

  // Drafts survive Back/Continue; the server holds what was saved.
  let provider = $state({ type: 'anthropic', name: 'anthropic', nameEdited: false, apiKey: '', baseURL: '', setDefault: true, saved: '', probe: null })
  let agent = $state({ name: 'assistant', model: '', tier: 'supervised', supervisorModel: '', supervisorTimeout: '30s', contextMessages: 5, saved: '' })
  let persona = $state({ displayName: '', emoji: '', tone: 'generalist', customTheme: '', rules: null, saved: false })
  let chat = $state({ app: 'telegram', token: '', bot: null, verify: null, sender: null, confirmed: false, manualID: '', manual: false, saved: false, restart: null })
  let restartRequired = $state(false)
  let restartInfo = $state({ available: false, managed: false })

  let step = $derived(STEPS[index])
  let agentLabel = $derived(persona.displayName || agent.saved || agent.name || 'your agent')
  let tierLabel = $derived(TIERS.find(t => t.id === agent.tier)?.label || '')
  let chatLabel = $derived(chat.saved ? providerLabelFor(chat.app) + (chat.bot ? ` · @${chat.bot.username}` : '') : '')

  function providerLabelFor(app) {
    return app === 'telegram' ? 'Telegram' : app === 'discord' ? 'Discord' : ''
  }

  const HEADERS = {
    welcome: { kicker: 'Welcome to Denkeeper', title: "Let's set up your first agent", sub: 'An agent is an AI assistant you talk to here, in Telegram or in Discord. Four short steps, and you can change all of it later.' },
    provider: { kicker: 'Step 1 of 4', title: 'Connect a provider', sub: "Pick where your agent's model runs. You can add more providers later." },
    agent: { kicker: 'Step 2 of 4', title: 'Create an agent', sub: 'Pick a name and a model. Everything here can be changed later.' },
    persona: { kicker: 'Step 3 of 4', title: 'Give it a personality', sub: 'How your agent introduces itself and talks to you. Watch the preview change.' },
    chat: { kicker: 'Step 4 of 4 · optional', title: 'Connect a chat app', sub: 'Talk to your agent from your phone. The web chat always works, so you can skip this.' },
    ready: { kicker: 'All set', title: '', sub: '' },
  }
  let header = $derived(step === 'ready'
    ? { kicker: 'All set', title: `${agentLabel} is ready`, sub: `Say hello here${chat.saved ? ' or in ' + providerLabelFor(chat.app) : ''}. You can change anything later under Agents.` }
    : HEADERS[step])

  let railSteps = $derived([
    {
      id: 'provider', label: 'Connect a provider',
      summary: provider.saved ? `${provider.saved} · ${provider.probe?.status === 'ok' ? 'key works' : 'saved'}` : 'Where the model runs',
    },
    { id: 'agent', label: 'Create an agent', summary: agent.saved ? `${agent.saved} · ${tierLabel}` : 'Model and permissions' },
    {
      id: 'persona', label: 'Give it a personality',
      summary: persona.saved ? `${persona.displayName} · ${TONES.find(t => t.id === persona.tone)?.label || 'Custom tone'}` : 'Name, emoji and tone',
    },
    { id: 'chat', label: 'Connect a chat app', optional: true, summary: chatLabel || (step === 'ready' ? 'Web chat only' : 'Telegram or Discord') },
  ].map(s => ({
    ...s,
    state: STEPS[index] === s.id ? 'current' : isDone(s.id) ? 'done' : 'upcoming',
  })))

  function isDone(id) {
    if (id === 'provider') return !!provider.saved
    if (id === 'agent') return !!agent.saved
    if (id === 'persona') return persona.saved
    return chat.saved || step === 'ready'
  }

  let preview = $derived({
    name: persona.displayName || agent.saved || (index >= 2 ? agent.name : ''),
    emoji: persona.emoji,
    model: agent.model && index >= 2 ? agent.model : '',
    provider: provider.saved || (index >= 1 && provider.probe?.status === 'ok' ? providerMeta(provider.type).label : ''),
    tier: index >= 2 ? agent.tier : '',
    supervised: index >= 2 && agent.tier === 'supervised',
    chatApp: chatLabel,
    greeting: index >= 3 && persona.displayName ? sampleGreeting(persona.tone, persona.displayName) : '',
    keyWorks: provider.probe?.status === 'ok',
  })

  onMount(async () => {
    const state = await refreshSetup()
    if (state) resume(state)
    loading = false
    await go(index)
  })

  // resume fills the drafts from server progress and opens the first step
  // that still needs doing.
  function resume(state) {
    const byID = Object.fromEntries(state.steps.map(s => [s.id, s]))
    const p = byID.provider
    if (p?.done) Object.assign(provider, { saved: p.detail.name, name: p.detail.name, type: p.detail.type || provider.type, nameEdited: true })
    const a = byID.agent
    if (a?.done) Object.assign(agent, { saved: a.detail.name, name: a.detail.name, model: a.detail.model || '', tier: a.detail.tier || agent.tier })
    const pe = byID.persona
    if (pe?.done) {
      const tone = toneForTheme(pe.detail.theme)
      Object.assign(persona, { saved: true, displayName: pe.detail.display_name, emoji: pe.detail.emoji || '', tone, customTheme: tone === 'custom' ? pe.detail.theme : '' })
    }
    const c = byID.chat_app
    if (c?.done) Object.assign(chat, { app: c.detail.type, saved: true })
    restartRequired = state.restartRequired
    restartInfo = state.restart

    if (!p?.done && !a?.done) index = 0
    else if (!p?.done) index = 1
    else if (!a?.done) index = 2
    else if (!pe?.done) index = 3
    else if (!c?.done) index = 4
    else index = 5
  }

  async function go(i) {
    index = i
    stepReady = false
    if (STEPS[i] === 'persona' && !persona.displayName) {
      const n = agent.saved || agent.name
      persona.displayName = n.charAt(0).toUpperCase() + n.slice(1)
    }
    await tick()
    heading?.focus()
  }

  async function next() {
    if (busy) return
    if (stepRef?.submit) {
      busy = true
      const ok = await stepRef.submit()
      busy = false
      if (!ok) return
    }
    if (step === 'chat') {
      const state = await refreshSetup()
      if (state) { restartRequired = state.restartRequired; restartInfo = state.restart }
    }
    go(index + 1)
  }

  function back() {
    if (index > 0 && !busy) go(index - 1)
  }

  function skipChat() {
    chat.app = 'web'
    go(STEPS.indexOf('ready'))
  }

  async function leave() {
    leaveError = ''
    busy = true
    try {
      await skipSetup()
    } catch (e) {
      leaveError = e.message
    } finally {
      busy = false
    }
  }

  async function finish(dest, prompt = '') {
    if (busy) return
    busy = true
    try {
      if (dest === 'chat') pendingSkillTest.set({ agent: agent.saved, command: prompt, send: false })
      await completeSetup()
      navigate(dest)
    } catch (e) {
      leaveError = e.message
    } finally {
      busy = false
    }
  }

  function onKeydown(e) {
    // Enter continues, except where Enter has its own meaning.
    if (e.key !== 'Enter' || e.shiftKey || busy || !stepReady) return
    if (['TEXTAREA', 'BUTTON', 'A'].includes(e.target.tagName)) return
    if (step === 'welcome' || step === 'ready') return
    e.preventDefault()
    next()
  }

  let canContinue = $derived(step === 'welcome' || stepReady)
  let continueLabel = $derived(step === 'welcome' ? 'Get started' : step === 'chat' ? (chat.app === 'web' ? 'Finish setup' : 'Save and finish') : 'Continue')
</script>

<svelte:window onkeydown={onKeydown} />

<div class="wizard" data-testid="setup-wizard">
  <aside class="rail" aria-label="Setup progress">
    <div class="rail-top">
      <div class="brand"><span class="brand-mark" aria-hidden="true"></span>Denkeeper</div>
      <WizardRail steps={railSteps} />
    </div>
    <div class="rail-preview">
      <span class="preview-label">{step === 'ready' ? 'Your agent' : 'Preview'}</span>
      <PreviewCard {...preview} />
    </div>
  </aside>

  <main class="main">
    <div class="topbar">
      {#if step !== 'welcome' && step !== 'ready'}
        <span class="mobile-progress">{['provider', 'agent', 'persona', 'chat'].indexOf(step) + 1} / 4 <span>{railSteps.find(s => s.id === step)?.label}</span></span>
      {/if}
      {#if step !== 'ready'}
        <button type="button" class="wz-link muted later" onclick={() => { confirmingLeave = true }} disabled={busy} data-testid="wizard-later">Set up later</button>
      {/if}
    </div>

    {#if confirmingLeave}
      <div class="leave wz-panel" role="group" aria-labelledby="leave-title" data-testid="wizard-leave-confirm">
        <p id="leave-title" class="wz-panel-title">Leave setup for now?</p>
        <p class="wz-hint">
          {provider.saved ? 'What you saved is kept.' : 'Nothing is saved yet.'}
          {agent.saved ? '' : "There's no agent yet, so chat won't work until you finish."}
          You can pick this up from the Overview page.
        </p>
        {#if leaveError}<p class="inline-error" role="alert">{leaveError}</p>{/if}
        <div class="leave-actions">
          <button type="button" class="btn-ghost" onclick={() => { confirmingLeave = false; leaveError = '' }} disabled={busy}>Keep going</button>
          <button type="button" class="btn-primary" onclick={leave} disabled={busy} data-testid="wizard-leave">Leave setup</button>
        </div>
      </div>
    {/if}

    <div class="mobile-preview"><PreviewCard {...preview} compact /></div>

    {#if loading}
      <p class="wz-hint loading">Loading…</p>
    {:else}
      <div class="form">
        <header class="head">
          {#if step === 'ready'}
            <span class="ready-avatar" aria-hidden="true">{persona.emoji || agentLabel.charAt(0).toUpperCase()}</span>
          {/if}
          <p class="kicker">{header.kicker}</p>
          <h1 tabindex="-1" bind:this={heading} class:big={step === 'welcome'}>{header.title}</h1>
          {#if header.sub}<p class="sub">{header.sub}</p>{/if}
        </header>

        {#if step === 'welcome'}
          <StepWelcome onUseConfigFile={leave} {busy} />
        {:else if step === 'provider'}
          <StepProvider bind:this={stepRef} bind:draft={provider} bind:ready={stepReady} />
        {:else if step === 'agent'}
          <StepAgent bind:this={stepRef} bind:draft={agent} bind:ready={stepReady} providerName={provider.saved} providerType={provider.type} models={provider.probe?.models || []} />
        {:else if step === 'persona'}
          <StepPersona bind:this={stepRef} bind:draft={persona} bind:ready={stepReady} agentName={agent.saved} />
        {:else if step === 'chat'}
          <StepChatApp bind:this={stepRef} bind:draft={chat} bind:ready={stepReady} agentName={agent.saved} displayName={persona.displayName} />
        {:else}
          <StepReady name={agentLabel} supervised={agent.tier === 'supervised'} chatApp={chat.saved ? chat.app : ''} {restartRequired} restart={restartInfo} onTry={p => finish('chat', p)} />
        {/if}
        {#if leaveError && !confirmingLeave}<p class="inline-error" role="alert">{leaveError}</p>{/if}
      </div>

      <footer class="footer">
        {#if step === 'ready'}
          <span></span>
          <div class="footer-right">
            <button type="button" class="btn-ghost" onclick={() => finish('overview')} disabled={busy} data-testid="wizard-dashboard">Go to dashboard</button>
            <button type="button" class="btn-primary primary" onclick={() => finish('chat')} disabled={busy} data-testid="wizard-open-chat">Open chat with {agentLabel}</button>
          </div>
        {:else}
          {#if index > 0}
            <button type="button" class="btn-ghost back" onclick={back} disabled={busy} aria-label="Back">
              <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><path d="M8.5 3L4.5 7l4 4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" /></svg>
              <span class="back-text">Back</span>
            </button>
          {:else}
            <span></span>
          {/if}
          <div class="footer-right">
            {#if step === 'chat' && chat.app !== 'web' && !chat.saved}
              <button type="button" class="wz-link muted" onclick={skipChat} disabled={busy} data-testid="wizard-skip-chat">Skip for now</button>
            {:else if step !== 'welcome'}
              <span class="enter-hint" aria-hidden="true">Enter ↵</span>
            {/if}
            <button type="button" class="btn-primary primary" onclick={next} disabled={busy || !canContinue} data-testid="wizard-continue">
              {busy ? 'Saving…' : continueLabel}
            </button>
          </div>
        {/if}
      </footer>
    {/if}
  </main>
</div>

<style>
  .wizard {
    display: flex;
    min-height: 100vh;
    background: var(--bg);
  }
  .rail {
    width: 440px;
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    gap: 32px;
    padding: 40px;
    background: var(--wizard-rail-bg);
    border-right: 1px solid var(--border);
    position: sticky;
    top: 0;
    height: 100vh;
    overflow-y: auto;
  }
  .rail-top { display: flex; flex-direction: column; gap: 48px; }
  .brand { display: flex; align-items: center; gap: 10px; font-size: 15px; font-weight: 700; }
  .brand-mark { width: 24px; height: 24px; border-radius: var(--radius); background: var(--accent); }
  .rail-preview { display: flex; flex-direction: column; gap: 14px; }
  .preview-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-muted);
  }

  .main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    padding: 40px 56px 48px 96px;
  }
  .topbar { display: flex; justify-content: flex-end; align-items: center; gap: 12px; min-height: 20px; }
  .mobile-progress, .mobile-preview { display: none; }
  .leave { max-width: 544px; margin-top: 16px; }
  .leave-actions { display: flex; justify-content: flex-end; gap: 10px; }

  .form {
    flex: 1;
    width: 100%;
    max-width: 544px;
    display: flex;
    flex-direction: column;
    gap: 28px;
    padding-top: 48px;
  }
  .head { display: flex; flex-direction: column; gap: 8px; }
  .kicker { font-size: 13px; font-weight: 600; color: var(--accent); }
  h1 { font-size: 34px; font-weight: 800; letter-spacing: -0.02em; line-height: 1.18; outline: none; }
  h1.big { font-size: 44px; letter-spacing: -0.03em; }
  .sub { font-size: 15px; color: var(--text-muted); }
  .ready-avatar {
    width: 72px;
    height: 72px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    background: rgba(var(--accent-rgb), 0.12);
    font-size: 34px;
    font-weight: 700;
    color: var(--accent);
    margin-bottom: 8px;
  }
  .loading { padding-top: 48px; }

  .footer {
    width: 100%;
    max-width: 544px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    padding-top: 40px;
  }
  .footer-right { display: flex; align-items: center; gap: 16px; }
  .footer .btn-primary, .footer .btn-ghost { font-size: 14px; font-weight: 600; padding: 10px 20px; }
  .back { display: inline-flex; align-items: center; gap: 6px; }
  .enter-hint { font-size: 12px; color: var(--text-muted); }

  @media (max-width: 1100px) {
    .rail { width: 340px; padding: 32px; }
    .main { padding: 32px 32px 40px 48px; }
  }

  @media (max-width: 768px) {
    .wizard { flex-direction: column; }
    .rail { display: none; }
    .main { padding: 0; }
    .topbar {
      justify-content: space-between;
      padding: 16px 20px 8px;
      background: var(--wizard-rail-bg);
    }
    .mobile-progress { display: inline; font-size: 13px; font-weight: 700; color: var(--accent); }
    .mobile-progress span { font-weight: 400; color: var(--text-muted); margin-left: 6px; }
    .mobile-preview {
      display: block;
      padding: 4px 20px 16px;
      background: var(--wizard-rail-bg);
      border-bottom: 1px solid var(--border);
    }
    .leave { margin: 12px 20px 0; }
    .form { padding: 24px 20px; gap: 22px; max-width: none; }
    h1 { font-size: 28px; }
    h1.big { font-size: 30px; }
    .footer {
      position: sticky;
      bottom: 0;
      max-width: none;
      padding: 12px 20px calc(12px + var(--safe-area-bottom));
      background: var(--surface);
      border-top: 1px solid var(--border);
    }
    .footer-right { flex: 1; justify-content: flex-end; }
    .footer .primary { flex: 1; min-height: 50px; font-size: 16px; }
    .enter-hint, .back-text { display: none; }
    .back { min-height: 50px; min-width: 52px; justify-content: center; }
  }
</style>
