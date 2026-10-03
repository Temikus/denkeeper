<script>
  import { setup, openWizard, nextStep } from '../setupStore.js'

  // Shown on Overview after "Set up later" until a working agent exists.
  // onhide hides the card; the sidebar reminder stays until setup is done.
  let { onhide } = $props()

  const LABELS = {
    provider: 'Connect a provider',
    agent: 'Create an agent',
    persona: 'Give it a personality',
    chat_app: 'Connect a chat app',
  }

  const SNIPPET = `[[llm.providers]]
name    = "anthropic"
type    = "anthropic"
api_key = "sk-ant-…"

[[agents]]
name         = "assistant"
llm_provider = "anthropic"
session_tier = "supervised"`

  let copied = $state(false)
  let next = $derived(nextStep($setup.steps))
  let hasAgent = $derived($setup.steps.some(s => s.id === 'agent' && s.done))
  let manualLink = $derived(next === 'provider' ? { href: '#/providers', text: 'Add a provider by hand' } : { href: '#/agents', text: 'Add an agent by hand' })

  function stepNote(s) {
    if (s.done) return 'Done'
    if (s.id === next) return 'Start here'
    if (s.optional) return 'Optional'
    if (s.id === 'persona') return 'Defaults are fine'
    return 'Needs step 1'
  }

  async function copySnippet() {
    try {
      await navigator.clipboard.writeText(SNIPPET)
      copied = true
      setTimeout(() => { copied = false }, 2000)
    } catch { /* clipboard blocked; the text is selectable */ }
  }
</script>

<section class="setup-card" aria-labelledby="setup-card-title" data-testid="setup-card">
  <div class="main">
    <div class="head">
      <p class="kicker">Setup skipped · {$setup.doneCount} of {$setup.total}</p>
      <h2 id="setup-card-title">{hasAgent ? 'Finish setting up your agent' : 'Denkeeper is running, but nothing can reply yet'}</h2>
      <p class="sub">
        {hasAgent ? 'Your agent works. A personality and a chat app make it yours.' : "There's no model provider and no agent. Chat, schedules and chat apps all need an agent first."}
      </p>
    </div>
    <ol class="steps">
      {#each $setup.steps as s, i (s.id)}
        <li class="step" class:done={s.done} class:next={s.id === next}>
          <span class="num" aria-hidden="true">{s.done ? '✓' : i + 1}</span>
          <span class="label">{LABELS[s.id]}</span>
          <span class="note">{stepNote(s)}</span>
        </li>
      {/each}
    </ol>
    <div class="actions">
      <button class="btn-primary" onclick={openWizard} data-testid="setup-card-resume">Resume setup</button>
      <a href={manualLink.href}>{manualLink.text}</a>
      <button class="hide" onclick={onhide} data-testid="setup-card-hide">Hide · stays in the sidebar</button>
    </div>
  </div>
  {#if !hasAgent}
    <div class="config">
      <p class="config-title">Prefer config files?</p>
      <p class="config-text">This is the smallest working setup. Add it to denkeeper.toml, then press Reload on the Server page.</p>
      <pre class="snippet">{SNIPPET}</pre>
      <p class="config-links">
        <button class="link" onclick={copySnippet}>{copied ? 'Copied' : 'Copy'}</button>
        ·
        <a href="https://denkeeper.io/docs/reference/config/" target="_blank" rel="noopener noreferrer">Full config reference ↗</a>
      </p>
    </div>
  {/if}
</section>

<style>
  .setup-card {
    display: flex;
    gap: 40px;
    padding: 28px 32px;
    margin-bottom: 24px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 4px solid var(--accent);
    border-radius: 12px;
  }
  .main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 18px; }
  .head { display: flex; flex-direction: column; gap: 6px; }
  .kicker { font-size: 12px; font-weight: 600; color: var(--accent); }
  h2 { font-size: 22px; font-weight: 800; letter-spacing: -0.02em; line-height: 1.25; }
  .sub { font-size: 14px; color: var(--text-muted); }

  .steps { list-style: none; border-top: 1px solid var(--border); }
  .step {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 0;
    border-bottom: 1px solid var(--border);
    font-size: 14px;
  }
  .num {
    width: 22px;
    height: 22px;
    flex-shrink: 0;
    border-radius: 50%;
    border: 1px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 11px;
    font-weight: 600;
    color: var(--text-muted);
  }
  .label { flex: 1; }
  .note { font-size: 13px; color: var(--text-muted); }
  .step.next .num { border: 2px solid var(--accent); color: var(--accent); }
  .step.next .label { font-weight: 600; }
  .step.next .note { color: var(--accent); font-weight: 500; }
  .step.done .num { background: var(--success); border-color: var(--success); color: #fff; }
  .step.done .label { color: var(--text-muted); }

  .actions { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; }
  .actions a { font-size: 14px; font-weight: 500; }
  .hide {
    margin-left: auto;
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    font-size: 13px;
    color: var(--text-muted);
    cursor: pointer;
  }
  .hide:hover { color: var(--text); text-decoration: underline; }

  .config {
    width: 340px;
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 18px;
    background: var(--bg);
    border-radius: 10px;
  }
  .config-title { font-size: 13px; font-weight: 600; }
  .config-text { font-size: 13px; color: var(--text-muted); }
  .snippet {
    margin: 0;
    padding: 12px 14px;
    background: #2a1f19;
    color: #f1e8da;
    border-radius: 8px;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
    line-height: 1.5;
    overflow-x: auto;
    user-select: all;
  }
  .config-links { font-size: 13px; color: var(--text-muted); }
  .link { background: none; border: none; padding: 0; font: inherit; color: var(--accent); cursor: pointer; font-weight: 500; }

  @media (max-width: 1100px) {
    .setup-card { flex-direction: column; gap: 20px; }
    .config { width: auto; }
  }
  @media (max-width: 768px) {
    .setup-card { padding: 18px; border-left-width: 1px; border-top: 4px solid var(--accent); }
    h2 { font-size: 20px; }
    .actions .btn-primary { width: 100%; min-height: 48px; font-size: 16px; }
    .hide { margin-left: 0; }
  }
</style>
