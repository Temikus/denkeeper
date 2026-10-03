<script>
  import { onMount } from 'svelte'
  import { api } from '../../api.js'
  import { TIERS, pickModel } from './wizardContent.js'

  // draft: { name, model, tier, supervisorModel, supervisorTimeout,
  //          contextMessages, saved }
  let {
    draft = $bindable(),
    ready = $bindable(false),
    providerName = '',
    providerType = '',
    models = [],
  } = $props()

  const NAME_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/
  const TIMEOUTS = ['15s', '30s', '60s', '120s']
  const CONTEXT_CHOICES = [2, 5, 10]

  let available = $state([])
  let taken = $state([])
  let changing = $state(false)
  let saving = $state(false)
  let error = $state('')

  let nameError = $derived(
    !draft.name ? '' : !NAME_RE.test(draft.name) ? 'Lowercase letters, numbers and hyphens only.'
      : !draft.saved && taken.includes(draft.name) ? 'An agent with this name already exists.' : '',
  )
  let nameOK = $derived(!!draft.name && !nameError)
  let supervised = $derived(draft.tier === 'supervised')
  let shortSup = $derived((draft.supervisorModel || 'the default model').replace(/^[a-z]+\//, ''))

  $effect(() => {
    // A saved agent may have no model of its own (it uses the default).
    ready = !saving && (!!draft.saved || (nameOK && !!draft.model.trim()))
  })

  onMount(async () => {
    available = models
    if (!available.length && providerName) {
      try {
        available = (await api.modelDetails(providerName)).map(m => m.id)
      } catch { /* the field still accepts a typed model ID */ }
    }
    if (!draft.model) draft.model = pickModel(providerType, available, 'main')
    if (!draft.supervisorModel) draft.supervisorModel = pickModel(providerType, available, 'supervisor')
    try {
      taken = (await api.agents()).map(a => a.name)
    } catch { /* the create call reports a duplicate */ }
  })

  function supervisorName() {
    return taken.includes('supervisor') || draft.name === 'supervisor' ? `${draft.name}-supervisor` : 'supervisor'
  }

  export async function submit() {
    error = ''
    if (draft.saved) return true
    if (!nameOK) {
      error = nameError || 'Give the agent a name.'
      return false
    }
    saving = true
    try {
      const body = {
        name: draft.name,
        llm_provider: providerName || undefined,
        llm_model: draft.model.trim(),
        session_tier: draft.tier,
      }
      if (supervised) {
        body.create_supervisor = {
          name: supervisorName(),
          llm_model: draft.supervisorModel.trim() || undefined,
          timeout: draft.supervisorTimeout,
          context_messages: draft.contextMessages,
        }
      }
      await api.createAgent(body)
      draft.saved = draft.name
      return true
    } catch (e) {
      error = e.message
      return false
    } finally {
      saving = false
    }
  }
</script>

<datalist id="wizard-models">
  {#each available as m (m)}<option value={m}></option>{/each}
</datalist>

<div class="wz-stack">
  {#if draft.saved}
    <p class="wz-hint">Agent <strong>{draft.saved}</strong> is already created. You can change its model and permissions later under Agents.</p>
  {:else}
    <div class="wz-row">
      <div class="wz-field name-field">
        <label class="wz-label" for="wizard-agent-name">Agent name</label>
        <div class="wz-input-wrap">
          <input
            id="wizard-agent-name"
            class="wz-input"
            class:ok={nameOK}
            class:bad={!!nameError}
            autocomplete="off"
            spellcheck="false"
            placeholder="assistant"
            bind:value={draft.name}
            disabled={saving}
            aria-invalid={!!nameError}
            aria-describedby="wizard-agent-name-msg"
            data-testid="wizard-agent-name"
          />
          {#if nameOK}
            <span class="wz-input-action ok-mark" aria-hidden="true">
              <svg width="16" height="16" viewBox="0 0 16 16"><path d="M3.5 8.3l3 3 6-6.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </span>
          {/if}
        </div>
        <span id="wizard-agent-name-msg" class="wz-status" class:bad={!!nameError} class:muted={!nameError}>{nameError || 'Used in commands and URLs.'}</span>
      </div>
      <div class="wz-field">
        <label class="wz-label" for="wizard-agent-model">Model</label>
        <input id="wizard-agent-model" class="wz-input" list="wizard-models" autocomplete="off" spellcheck="false" bind:value={draft.model} disabled={saving} data-testid="wizard-agent-model" />
        <span class="wz-status muted">{available.length ? `${available.length} models from ${providerName}` : 'Type a model ID'}</span>
      </div>
    </div>

    <fieldset class="wz-field">
      <legend class="wz-label">Autonomy</legend>
      <div class="wz-segmented" data-testid="wizard-agent-tier">
        {#each TIERS as t (t.id)}
          <label class="wz-segment">
            <input type="radio" name="tier" value={t.id} bind:group={draft.tier} disabled={saving} />
            {t.label}
          </label>
        {/each}
      </div>
      <span class="wz-status muted">{TIERS.find(t => t.id === draft.tier)?.caption}</span>
    </fieldset>

    {#if supervised}
      <div class="wz-panel" data-testid="wizard-supervisor-callout">
        <p class="wz-panel-title">When {draft.name || 'the agent'} wants to use a tool</p>
        <ol class="flow">
          <li class="flow-step"><span class="flow-who">{draft.name || 'agent'}</span><span class="flow-what">asks to run a tool</span></li>
          <li class="flow-step accent"><span class="flow-who">supervisor</span><span class="flow-what">checks the call</span></li>
          <li class="flow-step you"><span class="flow-who">you</span><span class="flow-what">only if it's unsure</span></li>
        </ol>
        {#if changing}
          <div class="sup-settings">
            <div class="wz-row">
              <div class="wz-field">
                <label class="wz-label" for="wizard-sup-model">Supervisor model</label>
                <input id="wizard-sup-model" class="wz-input" list="wizard-models" bind:value={draft.supervisorModel} disabled={saving} data-testid="wizard-supervisor-model" />
                <span class="wz-hint">A small, fast model is enough.</span>
              </div>
              <div class="wz-field timeout">
                <label class="wz-label" for="wizard-sup-timeout">Waits up to</label>
                <select id="wizard-sup-timeout" class="wz-input" bind:value={draft.supervisorTimeout} disabled={saving}>
                  {#each TIMEOUTS as t (t)}<option value={t}>{t.replace('s', ' seconds')}</option>{/each}
                </select>
              </div>
            </div>
            <fieldset class="wz-field">
              <legend class="wz-label">Context it reads</legend>
              <div class="wz-segmented">
                {#each CONTEXT_CHOICES as n (n)}
                  <label class="wz-segment">
                    <input type="radio" name="context" value={n} bind:group={draft.contextMessages} disabled={saving} />
                    Last {n}
                  </label>
                {/each}
              </div>
            </fieldset>
            <div class="sup-footer">
              <span class="wz-hint">Saved as a second agent called "{supervisorName()}".</span>
              <button type="button" class="wz-link" onclick={() => { changing = false }}>Done</button>
            </div>
          </div>
        {:else}
          <div class="sup-footer">
            <span class="wz-hint">Supervisor uses {shortSup} and waits up to {draft.supervisorTimeout}.</span>
            <button type="button" class="wz-link" onclick={() => { changing = true }} data-testid="wizard-supervisor-change">Change</button>
          </div>
        {/if}
      </div>
    {/if}
  {/if}

  {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
</div>

<style>
  .name-field { flex: 0 0 220px !important; }
  .ok-mark { color: var(--success); pointer-events: none; }
  fieldset { border: none; min-width: 0; }
  legend { margin-bottom: 6px; }

  .flow {
    list-style: none;
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 26px;
  }
  .flow-step {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 10px 12px;
    background: #fff;
    border: 1px solid var(--border);
    border-radius: 8px;
  }
  :global(:root.dark) .flow-step { background: var(--bg); }
  .flow-step:not(:last-child)::after {
    content: '→';
    position: absolute;
    right: -20px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--accent);
  }
  .flow-step.accent { background: rgba(var(--accent-rgb), 0.08); border-color: rgba(var(--accent-rgb), 0.35); }
  .flow-step.accent .flow-who { color: var(--accent); }
  .flow-step.you { border-style: dashed; }
  .flow-who { font-size: 13px; font-weight: 600; }
  .flow-what { font-size: 12px; color: var(--text-muted); }

  .sup-settings { display: flex; flex-direction: column; gap: 16px; }
  .timeout { flex: 0 0 150px !important; }
  .sup-footer { display: flex; justify-content: space-between; align-items: baseline; gap: 12px; }

  @media (max-width: 768px) {
    .name-field, .timeout { flex: 1 1 auto !important; }
    .flow { grid-template-columns: 1fr; gap: 10px; }
    .flow-step:not(:last-child)::after { content: '↓'; right: auto; left: 12px; top: auto; bottom: -17px; transform: none; }
  }
</style>
