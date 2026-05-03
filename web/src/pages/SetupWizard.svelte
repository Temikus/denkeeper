<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'

  let { onComplete } = $props()

  // step: 'provider' | 'agent' | 'persona'
  let step = $state('provider')
  let loading = $state(true)

  // Cross-step state
  let providerName = $state('')
  let agentName = $state('')

  // --- Provider step ---
  let providerType = $state('anthropic')
  let providerFormName = $state('anthropic')
  let providerAPIKey = $state('')
  let providerBaseURL = $state('')
  let providerSetDefault = $state(true)
  let providerError = $state('')
  let providerSaving = $state(false)

  const providerTypes = [
    { value: 'anthropic', label: 'Anthropic' },
    { value: 'openai', label: 'OpenAI' },
    { value: 'openrouter', label: 'OpenRouter' },
    { value: 'ollama', label: 'Ollama' },
  ]

  function onProviderTypeChange() {
    providerFormName = providerType
  }

  // --- Agent step ---
  let agentFormName = $state('')
  let agentProvider = $state('')
  let agentModel = $state('')
  let agentTier = $state('supervised')
  let agentDescription = $state('')
  let agentError = $state('')
  let agentSaving = $state(false)
  let providers = $state([])
  let defaultProvider = $state('')

  // Supervisor companion
  let supervisorName = $state('supervisor')
  let supervisorModel = $state('')
  let supervisorTimeout = $state('30s')
  let supervisorContextMsgs = $state(5)

  // --- Persona step ---
  let displayName = $state('')
  let emoji = $state('')
  let theme = $state('helpful general-purpose assistant')
  let behaviorGuidelines = $state(
`Be genuinely helpful, not performatively helpful. Skip filler — just help.

Have opinions. You're allowed to disagree, prefer things, find stuff amusing or boring.

Be resourceful before asking. Try to figure it out first, then ask if stuck.

Earn trust through competence. Be careful with external actions. Be bold with internal ones.

Remember you're a guest. You have access to someone's life — treat it with respect.`)
  let personaError = $state('')
  let personaSaving = $state(false)

  const STORAGE_KEY = 'dk_wizard_state'

  function saveState() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ step, providerName, agentName }))
    } catch { /* ignore */ }
  }

  onMount(async () => {
    try {
      const saved = localStorage.getItem(STORAGE_KEY)
      if (saved) {
        const s = JSON.parse(saved)
        if (s.step === 'agent' && s.providerName) {
          providerName = s.providerName
          step = 'agent'
        } else if (s.step === 'persona' && s.agentName) {
          agentName = s.agentName
          providerName = s.providerName || ''
          step = 'persona'
        }
      }
    } catch { /* ignore */ }
    loading = false
  })

  // --- Provider submit ---
  async function submitProvider() {
    providerError = ''
    const name = providerFormName.trim()
    if (!name) { providerError = 'Name is required.'; return }
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(name)) {
      providerError = 'Must be lowercase alphanumeric with hyphens only.'
      return
    }

    providerSaving = true
    try {
      const body = { name, type: providerType }
      if (providerAPIKey) body.api_key = providerAPIKey
      if (providerBaseURL) body.base_url = providerBaseURL
      await api.createLLMProvider(body)
      if (providerSetDefault) {
        await api.updateLLMConfig({ default_provider: name })
      }
      providerName = name
      step = 'agent'
      saveState()
      await fetchProviders()
    } catch (e) {
      providerError = e.message
    } finally {
      providerSaving = false
    }
  }

  // --- Agent step helpers ---
  async function fetchProviders() {
    try {
      const data = await api.llmProviders()
      providers = data.providers || []
      defaultProvider = data.default_provider || ''
      if (!agentProvider) agentProvider = defaultProvider
    } catch { /* ignore */ }
  }

  $effect(() => {
    if (step === 'agent') fetchProviders()
  })

  function providerLabel(p) {
    if (p.name === defaultProvider) return `Default (${p.name})`
    return p.name
  }

  async function submitAgent() {
    agentError = ''
    const name = agentFormName.trim()
    if (!name) { agentError = 'Agent name is required.'; return }
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(name)) {
      agentError = 'Must be lowercase letters, numbers, and hyphens only.'
      return
    }

    agentSaving = true
    try {
      const body = {
        name,
        llm_provider: agentProvider || undefined,
        llm_model: agentModel || undefined,
        session_tier: agentTier,
        description: agentDescription || undefined,
      }
      if (agentTier === 'supervised') {
        const supName = supervisorName.trim()
        if (supName && supName !== name) {
          body.create_supervisor = {
            name: supName,
            llm_model: supervisorModel || undefined,
            timeout: supervisorTimeout || '30s',
            context_messages: supervisorContextMsgs || 5,
          }
        }
      }
      await api.createAgent(body)
      agentName = name
      displayName = name.charAt(0).toUpperCase() + name.slice(1)
      step = 'persona'
      saveState()
    } catch (e) {
      agentError = e.message
    } finally {
      agentSaving = false
    }
  }

  // --- Persona submit ---
  async function submitPersona() {
    personaError = ''
    personaSaving = true
    try {
      const identityYaml = `---\nname: "${displayName}"\nemoji: "${emoji}"\ntheme: "${theme}"\n---`
      await api.updatePersona(agentName, 'identity', identityYaml)
      await api.updatePersona(agentName, 'soul', behaviorGuidelines)
      await finishWizard()
    } catch (e) {
      personaError = e.message
    } finally {
      personaSaving = false
    }
  }

  async function useDefaults() {
    personaSaving = true
    try {
      await finishWizard()
    } catch (e) {
      personaError = e.message
    } finally {
      personaSaving = false
    }
  }

  async function finishWizard() {
    await api.wizardComplete()
    try { await api.reloadConfig() } catch { /* best effort */ }
    localStorage.removeItem(STORAGE_KEY)
    onComplete?.()
  }

  async function skipSetup() {
    try {
      await api.wizardComplete()
      localStorage.removeItem(STORAGE_KEY)
      onComplete?.()
    } catch { /* ignore */ }
  }
</script>

<div class="wizard-page">
  <div class="card" class:wide={step === 'persona'}>
    {#if loading}
      <h1>Loading...</h1>
    {:else if step === 'provider'}
      <h1>Add a Provider</h1>
      <p class="subtitle">Connect an LLM provider so your agents can think. You can add more later.</p>

      {#if providerError}
        <p class="error">{providerError}</p>
      {/if}

      <span class="field-label">Provider type</span>
      <select class="input" bind:value={providerType} onchange={onProviderTypeChange} disabled={providerSaving} data-testid="wizard-provider-type">
        {#each providerTypes as { value, label }}
          <option {value}>{label}</option>
        {/each}
      </select>

      <span class="field-label">Name</span>
      <input type="text" class="input" bind:value={providerFormName} disabled={providerSaving} placeholder="e.g. anthropic" data-testid="wizard-provider-name" />
      <span class="hint">Used to reference this provider in agent config</span>

      {#if providerType !== 'ollama'}
        <span class="field-label">API key</span>
        <input type="password" class="input" bind:value={providerAPIKey} disabled={providerSaving} placeholder={providerType === 'anthropic' ? 'sk-ant-api03-...' : 'sk-...'} data-testid="wizard-provider-apikey" />
      {/if}

      <span class="field-label">Base URL <span class="optional">optional</span></span>
      <input type="url" class="input" bind:value={providerBaseURL} disabled={providerSaving} placeholder={providerType === 'ollama' ? 'http://localhost:11434' : `https://api.${providerType}.com`} data-testid="wizard-provider-baseurl" />

      <label class="toggle-row">
        <span class="toggle-switch" class:on={providerSetDefault} role="switch" aria-checked={providerSetDefault} tabindex="0" onclick={() => providerSetDefault = !providerSetDefault} onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); providerSetDefault = !providerSetDefault } }}>
          <span class="toggle-knob"></span>
        </span>
        <span>Set as default provider</span>
      </label>

      <button onclick={submitProvider} disabled={providerSaving || !providerFormName.trim()} data-testid="wizard-provider-submit">
        {providerSaving ? 'Adding...' : 'Add provider'}
      </button>

    {:else if step === 'agent'}
      <h1>Create an Agent</h1>
      <p class="subtitle">Agents are independent personalities with their own LLM, permissions, and skills.</p>

      {#if agentError}
        <p class="error">{agentError}</p>
      {/if}

      <span class="field-label">Agent name</span>
      <input type="text" class="input" bind:value={agentFormName} disabled={agentSaving} placeholder="e.g. assistant, researcher" data-testid="wizard-agent-name" />
      <span class="hint">Lowercase letters, numbers, and hyphens only</span>

      <span class="field-label">LLM provider</span>
      <select class="input" bind:value={agentProvider} disabled={agentSaving} data-testid="wizard-agent-provider">
        {#each providers as p}
          <option value={p.name}>{providerLabel(p)}</option>
        {/each}
      </select>
      <span class="hint">Inherits global default if not set</span>

      <span class="field-label">Model</span>
      <input type="text" class="input" bind:value={agentModel} disabled={agentSaving} placeholder="e.g. claude-sonnet-4-20250514" data-testid="wizard-agent-model" />
      <span class="hint">Leave blank to use the global default model</span>

      <span class="field-label">Permission tier</span>
      <div class="tier-options">
        <label class="tier-option" class:selected={agentTier === 'autonomous'}>
          <input type="radio" name="tier" value="autonomous" bind:group={agentTier} disabled={agentSaving} />
          <div class="tier-content">
            <span class="tier-name">Autonomous</span>
            <span class="tier-desc">All actions allowed without approval</span>
          </div>
        </label>
        <label class="tier-option" class:selected={agentTier === 'supervised'}>
          <input type="radio" name="tier" value="supervised" bind:group={agentTier} disabled={agentSaving} />
          <div class="tier-content">
            <span class="tier-name">Supervised <span class="badge">DEFAULT</span></span>
            <span class="tier-desc">Tool calls require human or supervisor approval</span>
          </div>
        </label>
        <label class="tier-option" class:selected={agentTier === 'restricted'}>
          <input type="radio" name="tier" value="restricted" bind:group={agentTier} disabled={agentSaving} />
          <div class="tier-content">
            <span class="tier-name">Restricted</span>
            <span class="tier-desc">Chat and read-only tools only</span>
          </div>
        </label>
      </div>

      {#if agentTier === 'supervised'}
        <div class="supervisor-callout" data-testid="wizard-supervisor-callout">
          <div class="callout-header">
            <svg class="callout-icon" viewBox="0 0 20 20" fill="currentColor" width="16" height="16"><path fill-rule="evenodd" d="M10 1a4.5 4.5 0 00-4.5 4.5V9H5a2 2 0 00-2 2v6a2 2 0 002 2h10a2 2 0 002-2v-6a2 2 0 00-2-2h-.5V5.5A4.5 4.5 0 0010 1zm3 8V5.5a3 3 0 10-6 0V9h6z" clip-rule="evenodd" /></svg>
            COMPANION SUPERVISOR
          </div>
          <p class="callout-desc">A lightweight agent will be auto-created to review tool calls before they execute.</p>

          <span class="field-label">Name</span>
          <input type="text" class="input" bind:value={supervisorName} disabled={agentSaving} data-testid="wizard-supervisor-name" />

          <span class="field-label">Model</span>
          <input type="text" class="input" bind:value={supervisorModel} disabled={agentSaving} placeholder="e.g. claude-haiku-4.5" data-testid="wizard-supervisor-model" />
          <span class="hint">Supervisors only make quick yes/no decisions</span>

          <div class="inline-fields">
            <div class="inline-field">
              <span class="field-label">Timeout</span>
              <input type="text" class="input" bind:value={supervisorTimeout} disabled={agentSaving} />
            </div>
            <div class="inline-field">
              <span class="field-label">Context messages</span>
              <input type="number" class="input" bind:value={supervisorContextMsgs} disabled={agentSaving} min="0" max="50" />
            </div>
          </div>

          <p class="callout-note">If the supervisor can't decide, the request escalates to human approval. You can customize this agent later.</p>
        </div>
      {/if}

      <span class="field-label">Description <span class="optional">optional</span></span>
      <input type="text" class="input" bind:value={agentDescription} disabled={agentSaving} placeholder="What does this agent do?" data-testid="wizard-agent-description" />

      <button onclick={submitAgent} disabled={agentSaving || !agentFormName.trim()} data-testid="wizard-agent-submit">
        {#if agentSaving}
          Creating...
        {:else if agentTier === 'supervised'}
          Create agent + supervisor
        {:else}
          Create agent
        {/if}
      </button>

    {:else if step === 'persona'}
      <h1>Personalize Your Agent</h1>
      <p class="subtitle">Your agent comes with sensible defaults. Review and customize its identity and behavior guidelines.</p>

      {#if personaError}
        <p class="error">{personaError}</p>
      {/if}

      <span class="section-label">IDENTITY</span>
      <p class="section-desc">How your agent appears in conversations.</p>

      <div class="inline-fields">
        <div class="inline-field" style="flex:2">
          <span class="field-label">Display name</span>
          <input type="text" class="input" bind:value={displayName} disabled={personaSaving} placeholder="e.g. Fennec" data-testid="wizard-persona-name" />
        </div>
        <div class="inline-field" style="flex:0 0 64px">
          <span class="field-label">Emoji</span>
          <input type="text" class="input" bind:value={emoji} disabled={personaSaving} maxlength="4" style="text-align:center" data-testid="wizard-persona-emoji" />
        </div>
      </div>

      <span class="field-label">Theme</span>
      <input type="text" class="input" bind:value={theme} disabled={personaSaving} placeholder="e.g. helpful general-purpose assistant" data-testid="wizard-persona-theme" />
      <span class="hint">A short phrase that sets the agent's tone</span>

      <span class="section-label">BEHAVIOR GUIDELINES</span>
      <p class="section-desc">Core instructions that shape how your agent thinks and acts. You can refine these anytime.</p>

      <textarea class="input guidelines-textarea" bind:value={behaviorGuidelines} disabled={personaSaving} rows="10" data-testid="wizard-persona-guidelines"></textarea>

      <div class="persona-actions">
        <button class="btn-secondary" onclick={useDefaults} disabled={personaSaving}>Use defaults</button>
        <button onclick={submitPersona} disabled={personaSaving} data-testid="wizard-persona-submit">
          {personaSaving ? 'Saving...' : 'Save & continue'}
        </button>
      </div>
    {/if}

    <button class="skip-link" onclick={skipSetup} type="button">Skip setup</button>
  </div>
</div>

<style>
  .wizard-page {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
    padding: 24px;
    background: var(--bg);
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 40px;
    width: min(420px, 90vw);
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .card.wide {
    width: min(480px, 90vw);
  }
  h1 { font-size: 22px; font-weight: 700; color: var(--accent); }
  .subtitle { color: var(--text-muted); font-size: 13px; }
  .error { color: var(--danger); font-size: 13px; }
  .field-label { font-size: 12px; color: var(--text-muted); margin-bottom: -8px; display: block; }
  .hint { font-size: 11px; color: var(--text-muted); margin-top: -8px; }
  .optional { font-weight: 400; font-style: italic; }

  .input {
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    font-size: 14px;
    font-family: inherit;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }
  .input:focus { border-color: var(--accent); }
  .input:disabled { opacity: 0.6; }
  select.input { cursor: pointer; }

  button {
    padding: 10px;
    background: var(--accent);
    color: #fff;
    border: none;
    border-radius: var(--radius);
    cursor: pointer;
    font-size: 14px;
    font-weight: 600;
  }
  button:hover:not(:disabled) { background: var(--accent-hover); }
  button:disabled { opacity: 0.6; cursor: default; }

  .btn-secondary {
    background: var(--surface);
    color: var(--text);
    border: 1px solid var(--border);
  }
  .btn-secondary:hover:not(:disabled) {
    background: var(--bg);
  }

  /* Toggle switch */
  .toggle-row {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 13px;
    cursor: pointer;
  }
  .toggle-switch {
    display: inline-flex;
    align-items: center;
    width: 36px;
    height: 20px;
    border-radius: 10px;
    background: var(--border);
    padding: 2px;
    cursor: pointer;
    transition: background 0.15s;
    flex-shrink: 0;
  }
  .toggle-switch.on { background: var(--accent); }
  .toggle-knob {
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: #fff;
    transition: transform 0.15s;
  }
  .toggle-switch.on .toggle-knob { transform: translateX(16px); }

  /* Permission tier radio buttons */
  .tier-options {
    display: flex;
    flex-direction: column;
    gap: 0;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
  }
  .tier-option {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 12px;
    cursor: pointer;
    border-bottom: 1px solid var(--border);
    transition: background 0.1s;
  }
  .tier-option:last-child { border-bottom: none; }
  .tier-option:hover { background: var(--hover-overlay); }
  .tier-option.selected {
    background: rgba(200, 78, 53, 0.06);
    border-left: 3px solid var(--accent);
    padding-left: 9px;
  }
  .tier-option input[type="radio"] {
    accent-color: var(--accent);
    margin: 0;
    flex-shrink: 0;
  }
  .tier-content {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }
  .tier-name { font-size: 14px; font-weight: 600; }
  .tier-desc { font-size: 12px; color: var(--text-muted); }
  .badge {
    display: inline-block;
    font-size: 10px;
    font-weight: 700;
    background: var(--accent);
    color: #fff;
    padding: 1px 5px;
    border-radius: 3px;
    vertical-align: middle;
    margin-left: 4px;
  }

  /* Supervisor companion callout */
  .supervisor-callout {
    border: 1px solid var(--accent);
    border-radius: var(--radius);
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 10px;
    background: rgba(200, 78, 53, 0.03);
  }
  .callout-header {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.5px;
    color: var(--accent);
  }
  .callout-icon { color: var(--accent); flex-shrink: 0; }
  .callout-desc { font-size: 13px; color: var(--text-muted); }
  .callout-note { font-size: 12px; color: var(--text-muted); line-height: 1.5; }

  /* Inline field pairs */
  .inline-fields {
    display: flex;
    gap: 12px;
  }
  .inline-field {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  /* Persona section labels */
  .section-label {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.5px;
    color: var(--accent);
  }
  .section-desc { font-size: 13px; color: var(--text-muted); margin-top: -8px; }

  .guidelines-textarea {
    resize: vertical;
    line-height: 1.6;
    min-height: 160px;
  }

  .persona-actions {
    display: flex;
    gap: 8px;
  }
  .persona-actions button { flex: 1; }

  .skip-link {
    background: none;
    color: var(--text-muted);
    font-size: 12px;
    font-weight: 400;
    text-align: center;
    padding: 4px;
    margin-top: 4px;
  }
  .skip-link:hover:not(:disabled) {
    background: none;
    color: var(--text);
    text-decoration: underline;
  }
</style>
