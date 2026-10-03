<script>
  import { onDestroy } from 'svelte'
  import { api } from '../../api.js'
  import { CHAT_APPS } from './wizardContent.js'

  // draft: { app: 'telegram'|'discord'|'web', token, bot, verify, sender,
  //          confirmed, manualID, manual, saved, restart }
  let { draft = $bindable(), ready = $bindable(false), agentName = '', displayName = '' } = $props()

  let verifying = $state(false)
  let pairing = $state(false)
  let pairMessage = $state('')
  let saving = $state(false)
  let error = $state('')
  let controller = null

  let app = $derived(CHAT_APPS.find(a => a.id === draft.app))
  let who = $derived(displayName || agentName || 'your agent')
  let userID = $derived(draft.confirmed && draft.sender ? draft.sender.id : draft.manualID.trim())
  let manualValid = $derived(/^\d+$/.test(draft.manualID.trim()))

  $effect(() => {
    ready = !saving && (draft.app === 'web' || draft.saved || (!!draft.bot && (draft.confirmed || manualValid)))
  })

  onDestroy(stopPairing)

  function stopPairing() {
    controller?.abort()
    controller = null
    pairing = false
  }

  function chooseApp(id) {
    if (id === draft.app) return
    stopPairing()
    Object.assign(draft, { app: id, token: '', bot: null, verify: null, sender: null, confirmed: false, manualID: '', manual: false, saved: false })
    pairMessage = ''
  }

  export async function verify() {
    const token = draft.token.trim()
    if (!token || verifying) return
    stopPairing()
    verifying = true
    draft.bot = null
    draft.sender = null
    draft.confirmed = false
    try {
      const res = await api.chatAppVerify(draft.app, token)
      draft.verify = res
      if (res.status === 'ok') {
        draft.bot = res.bot
        if (draft.app === 'telegram') pair()
        else draft.manual = true
      }
    } catch (e) {
      draft.verify = { status: 'error', message: e.message }
    } finally {
      verifying = false
    }
  }

  // pair long-polls until a sender shows up or something other than a
  // timeout comes back. Each call waits up to 25s server-side.
  async function pair() {
    stopPairing()
    controller = new AbortController()
    const signal = controller.signal
    pairing = true
    pairMessage = ''
    let cursor = ''
    try {
      while (!signal.aborted) {
        const res = await api.chatAppPair({ type: draft.app, token: draft.token.trim(), cursor }, signal)
        if (res.status === 'found') {
          draft.sender = res.sender
          break
        }
        if (res.status !== 'timeout') {
          pairMessage = res.message
          draft.manual = true
          break
        }
        cursor = res.cursor
      }
    } catch (e) {
      if (e.name !== 'AbortError') {
        pairMessage = e.message
        draft.manual = true
      }
    } finally {
      if (!signal.aborted) pairing = false
    }
  }

  export async function submit() {
    error = ''
    if (draft.app === 'web' || draft.saved) return true
    stopPairing()
    saving = true
    try {
      const res = await api.chatAppSave({
        type: draft.app,
        token: draft.token.trim(),
        allowed_users: [userID],
        agent: agentName || undefined,
        notify_chat_id: draft.confirmed ? draft.sender?.chat_id : undefined,
      })
      draft.saved = true
      draft.restart = res.restart
      return true
    } catch (e) {
      error = e.message
      return false
    } finally {
      saving = false
    }
  }
</script>

<div class="wz-stack">
  <div class="wz-segmented" role="radiogroup" aria-label="Chat app" data-testid="wizard-chat-app">
    {#each [...CHAT_APPS, { id: 'web', label: 'Web chat only' }] as a (a.id)}
      <label class="wz-segment">
        <input type="radio" name="chat-app" value={a.id} checked={draft.app === a.id} onchange={() => chooseApp(a.id)} disabled={saving} />
        {a.label}
      </label>
    {/each}
  </div>

  {#if draft.app === 'web'}
    <p class="wz-hint">You'll chat with {who} in this dashboard. You can connect Telegram or Discord later from Agents.</p>
  {:else if draft.saved}
    <p class="wz-status ok">{app.label} is saved. It connects after the server restarts.</p>
  {:else}
    <ol class="steps">
      <li class="step" class:done={!!draft.bot}>
        <span class="num" aria-hidden="true">1</span>
        <div class="body">
          <div class="step-head">
            <span class="step-title">{draft.app === 'telegram' ? 'Create a bot with @BotFather' : 'Create a bot in the developer portal'}</span>
            <a href={app.sourceURL} target="_blank" rel="noopener noreferrer">Open {draft.app === 'telegram' ? 'BotFather' : 'portal'} ↗</a>
          </div>
          <span class="wz-hint">{draft.app === 'telegram' ? 'In Telegram, send /newbot and pick a name.' : 'Add a bot to an application and copy its token.'}</span>
        </div>
      </li>

      <li class="step" class:done={!!draft.bot}>
        <span class="num" aria-hidden="true">2</span>
        <div class="body">
          <label class="step-title" for="wizard-chat-token">Paste the bot token</label>
          <div class="token-row">
            <input
              id="wizard-chat-token"
              class="wz-input mono"
              class:ok={!!draft.bot}
              class:bad={draft.verify?.status === 'rejected'}
              type="password"
              autocomplete="off"
              spellcheck="false"
              placeholder={app.tokenPlaceholder}
              bind:value={draft.token}
              onpaste={() => setTimeout(verify, 0)}
              disabled={saving}
              data-testid="wizard-chat-token"
            />
            <button type="button" class="btn-ghost" onclick={verify} disabled={verifying || !draft.token.trim()}>{verifying ? 'Checking…' : 'Check'}</button>
          </div>
          <div aria-live="polite">
            {#if draft.bot}
              <p class="wz-status ok">Connected as @{draft.bot.username}</p>
            {:else if draft.verify && draft.verify.status !== 'ok'}
              <p class="wz-status bad">{draft.verify.message}</p>
            {/if}
          </div>
        </div>
      </li>

      <li class="step current">
        <span class="num" aria-hidden="true">3</span>
        <div class="body">
          {#if draft.app === 'telegram' && !draft.manual}
            <span class="step-title">Say hi to {draft.bot ? '@' + draft.bot.username : 'your bot'}</span>
            <span class="wz-hint">So {who} knows which Telegram account is yours. Nobody else can talk to it.</span>
            <div aria-live="polite">
              {#if draft.sender}
                <div class="sender" data-testid="wizard-pair-sender">
                  <div class="sender-who">
                    <span class="sender-name">{draft.sender.first_name || ''}{draft.sender.username ? ' · @' + draft.sender.username : ''}</span>
                    <span class="wz-hint">Sent "{draft.sender.text}" · ID {draft.sender.id}</span>
                  </div>
                  {#if draft.confirmed}
                    <span class="wz-status ok">That's you</span>
                  {:else}
                    <button type="button" class="btn-ghost" onclick={() => { draft.confirmed = true }} data-testid="wizard-pair-confirm">That's me</button>
                  {/if}
                </div>
                {#if !draft.confirmed}
                  <button type="button" class="wz-link muted" onclick={pair}>Not you? Wait for another message</button>
                {/if}
              {:else if pairing}
                <p class="wz-status muted listening"><span class="dot" aria-hidden="true"></span>Listening for a message to @{draft.bot.username}…</p>
                <a class="wz-link" href="https://t.me/{draft.bot.username}" target="_blank" rel="noopener noreferrer">Open t.me/{draft.bot.username} ↗</a>
              {/if}
            </div>
            <button type="button" class="wz-link muted" onclick={() => { stopPairing(); draft.manual = true }}>Enter a user ID by hand</button>
          {:else}
            <label class="step-title" for="wizard-chat-userid">Your {app.label} user ID</label>
            {#if pairMessage}<p class="wz-status warn">{pairMessage}</p>{/if}
            {#if draft.app === 'discord'}
              <span class="wz-hint">
                Share a server with the bot first{#if draft.bot?.invite_url}: <a href={draft.bot.invite_url} target="_blank" rel="noopener noreferrer">invite it ↗</a>{/if}.
                Then turn on Developer Mode, right-click your name and choose Copy User ID.
              </span>
            {:else}
              <span class="wz-hint">Message @userinfobot in Telegram and it replies with your numeric ID.</span>
            {/if}
            <input id="wizard-chat-userid" class="wz-input mono" inputmode="numeric" bind:value={draft.manualID} disabled={saving} data-testid="wizard-chat-userid" />
            {#if draft.app === 'telegram' && draft.bot}
              <button type="button" class="wz-link muted" onclick={() => { draft.manual = false; pair() }}>Detect it from a message instead</button>
            {/if}
          {/if}
        </div>
      </li>
    </ol>
  {/if}

  {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
</div>

<style>
  .steps { list-style: none; display: flex; flex-direction: column; }
  .step { position: relative; display: flex; gap: 14px; padding-bottom: 22px; }
  .step:last-child { padding-bottom: 0; }
  .step:not(:last-child)::after {
    content: '';
    position: absolute;
    left: 10px;
    top: 26px;
    bottom: 4px;
    width: 2px;
    background: var(--border);
  }
  .num {
    flex-shrink: 0;
    width: 22px;
    height: 22px;
    border-radius: 50%;
    border: 1px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 11px;
    font-weight: 600;
    color: var(--text-muted);
    background: var(--bg);
  }
  .step.done .num { background: var(--success); border-color: var(--success); color: #fff; }
  .step.current:not(.done) .num { border: 2px solid var(--accent); color: var(--accent); }
  .body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 8px; }
  .step-head { display: flex; justify-content: space-between; align-items: baseline; gap: 12px; flex-wrap: wrap; }
  .step-title { font-size: 14px; font-weight: 600; }
  .token-row { display: flex; gap: 8px; }
  .token-row .wz-input { flex: 1; min-width: 0; }
  .sender {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: #fff;
  }
  :global(:root.dark) .sender { background: var(--bg); }
  .sender-who { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
  .sender-name { font-size: 14px; font-weight: 600; }
  .listening { align-items: center; }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--accent);
    box-shadow: 0 0 0 4px rgba(var(--accent-rgb), 0.2);
    animation: pulse 1.6s ease-in-out infinite;
  }
  @keyframes pulse { 50% { box-shadow: 0 0 0 7px rgba(var(--accent-rgb), 0.08); } }
  @media (prefers-reduced-motion: reduce) { .dot { animation: none; } }
</style>
