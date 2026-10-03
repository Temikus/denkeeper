<script>
  import { onMount, onDestroy } from 'svelte'
  import { api } from '../../api.js'
  import { PROVIDERS, providerMeta } from './wizardContent.js'

  // draft: { type, name, nameEdited, apiKey, baseURL, setDefault, saved,
  //          probe: {status, message, models} }
  // saved is the instance name once it is on the server (also on resume).
  let { draft = $bindable(), ready = $bindable(false) } = $props()

  const NAME_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/

  let existing = $state([]) // provider names already configured
  let showKey = $state(false)
  let advancedOpen = $state(false)
  let replacingKey = $state(false)
  let testing = $state(false)
  let saving = $state(false)
  let error = $state('')
  let saveAnyway = $state(false)
  let lastTested = ''
  let controller = null
  let seq = 0

  let meta = $derived(providerMeta(draft.type))
  let resumed = $derived(!!draft.saved && draft.saved === draft.name && !replacingKey)
  let needsKey = $derived(meta.needsKey && !resumed)
  let rejected = $derived(draft.probe?.status === 'rejected')
  let unreachable = $derived(draft.probe?.status === 'unreachable')

  $effect(() => {
    const keyGiven = !needsKey || !!draft.apiKey.trim()
    const probeBlocks = testing || ((rejected || unreachable) && !saveAnyway)
    ready = keyGiven && !probeBlocks && !saving
  })

  onMount(async () => {
    try {
      const data = await api.llmProviders()
      existing = (data.providers || []).map(p => p.name)
    } catch { /* the save reports any conflict */ }
    if (draft.type === 'ollama' && !draft.probe?.status) checkKey()
  })
  onDestroy(() => controller?.abort())

  function selectType(type) {
    if (type === draft.type) return
    draft.type = type
    if (!draft.nameEdited) draft.name = type
    draft.baseURL = ''
    draft.probe = null
    saveAnyway = false
    lastTested = ''
    if (type === 'ollama') checkKey()
  }

  function probeKey() {
    return `${draft.type}|${draft.apiKey}|${draft.baseURL}`
  }

  // test checks the key (or the stored key on resume) without saving it.
  export async function checkKey() {
    if (needsKey && !draft.apiKey.trim()) return
    controller?.abort()
    controller = new AbortController()
    const mine = ++seq
    testing = true
    saveAnyway = false
    lastTested = probeKey()
    const body = resumed && !draft.apiKey
      ? { name: draft.saved }
      : { type: draft.type, api_key: draft.apiKey.trim() || undefined, base_url: draft.baseURL.trim() || undefined }
    try {
      const res = await api.testLLMProvider(body, controller.signal)
      if (mine === seq) draft.probe = res
    } catch (e) {
      if (mine === seq && e.name !== 'AbortError') draft.probe = { status: 'error', message: e.message }
    } finally {
      if (mine === seq) testing = false
    }
  }

  function onKeyPaste() {
    setTimeout(checkKey, 0) // the bound value updates after the paste event
  }

  function onKeyBlur() {
    const testable = needsKey ? !!draft.apiKey.trim() : true
    if (testable && probeKey() !== lastTested) checkKey()
  }

  // submit saves the provider and reports whether to move on.
  export async function submit() {
    error = ''
    const name = draft.name.trim()
    if (!NAME_RE.test(name)) {
      error = 'Name must be lowercase letters, numbers and hyphens.'
      advancedOpen = true
      return false
    }
    if (resumed) return true
    saving = true
    try {
      const fields = {
        api_key: draft.apiKey.trim() || undefined,
        base_url: draft.baseURL.trim() || undefined,
      }
      if (existing.includes(name)) {
        await api.updateLLMProvider(name, fields)
        if (draft.setDefault) await api.updateLLMConfig({ default_provider: name })
      } else {
        const res = await api.createLLMProvider({ name, type: draft.type, ...fields })
        if (draft.setDefault && !res?.default) await api.updateLLMConfig({ default_provider: name })
        existing = [...existing, name]
      }
      draft.saved = name
      replacingKey = false
      return true
    } catch (e) {
      error = e.message
      return false
    } finally {
      saving = false
    }
  }

  let advancedSummary = $derived(
    `Saved as "${draft.name || draft.type}" · ${draft.setDefault ? 'default provider' : 'not default'} · ${draft.baseURL ? 'custom URL' : 'standard URL'}`,
  )
</script>

<div class="wz-stack">
  <fieldset class="wz-choices" style="--wz-cols: 4">
    <legend class="sr-only">Provider</legend>
    {#each PROVIDERS as p (p.type)}
      <label class="wz-choice" data-testid="wizard-provider-{p.type}">
        <input type="radio" name="provider-type" value={p.type} checked={draft.type === p.type} onchange={() => selectType(p.type)} disabled={saving} />
        <span class="wz-choice-title">{p.label}</span>
        <span class="wz-choice-caption">{p.caption}</span>
      </label>
    {/each}
  </fieldset>

  {#if resumed}
    <div class="wz-field">
      <span class="wz-label">{meta.label}</span>
      <p class="wz-hint">
        Using <strong>{draft.saved}</strong>, already saved.
        <button type="button" class="wz-link" onclick={checkKey} disabled={testing}>Test it</button>
        ·
        <button type="button" class="wz-link" onclick={() => { replacingKey = true; draft.probe = null }}>Use a different key</button>
      </p>
    </div>
  {:else if meta.needsKey}
    <div class="wz-field">
      <label class="wz-label" for="wizard-key">
        <span>{meta.label} API key</span>
        <a href={meta.keyURL} target="_blank" rel="noopener noreferrer">Get a key ↗</a>
      </label>
      <div class="wz-input-wrap">
        <input
          id="wizard-key"
          class="wz-input mono"
          class:ok={draft.probe?.status === 'ok'}
          class:bad={rejected}
          type={showKey ? 'text' : 'password'}
          autocomplete="off"
          spellcheck="false"
          placeholder={meta.keyPlaceholder}
          bind:value={draft.apiKey}
          onpaste={onKeyPaste}
          onblur={onKeyBlur}
          disabled={saving}
          data-testid="wizard-provider-apikey"
        />
        <button type="button" class="wz-input-action" onclick={() => { showKey = !showKey }} aria-label={showKey ? 'Hide key' : 'Show key'} aria-pressed={showKey}>
          <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><path d="M1.5 8s2.4-4.5 6.5-4.5S14.5 8 14.5 8s-2.4 4.5-6.5 4.5S1.5 8 1.5 8z" fill="none" stroke="currentColor" stroke-width="1.4" /><circle cx="8" cy="8" r="2" fill="none" stroke="currentColor" stroke-width="1.4" /></svg>
        </button>
      </div>
    </div>
  {:else}
    <div class="wz-field">
      <label class="wz-label" for="wizard-ollama-url">Ollama address</label>
      <input id="wizard-ollama-url" class="wz-input mono" type="url" placeholder={meta.defaultBaseURL} bind:value={draft.baseURL} onblur={onKeyBlur} disabled={saving} data-testid="wizard-provider-baseurl" />
    </div>
  {/if}

  <div class="probe" aria-live="polite" data-testid="wizard-provider-status">
    {#if testing}
      <p class="wz-status muted"><span class="spinner" aria-hidden="true"></span>Checking with {meta.label}…</p>
    {:else if draft.probe?.status === 'ok'}
      <div class="probe-row">
        <p class="wz-status ok">{draft.probe.message}</p>
        <button type="button" class="wz-link muted" onclick={checkKey}>Test again</button>
      </div>
    {:else if rejected}
      <p class="wz-status bad">{draft.probe.message}</p>
      <p class="probe-actions">
        <a class="wz-link" href={meta.keyURL} target="_blank" rel="noopener noreferrer">Get a new key ↗</a>
        <button type="button" class="wz-link muted" onclick={() => { saveAnyway = true }} disabled={saveAnyway} data-testid="wizard-save-anyway">{saveAnyway ? 'Will save anyway' : 'Save anyway'}</button>
      </p>
    {:else if unreachable}
      <p class="wz-status warn">{draft.probe.message}</p>
      <p class="probe-actions">
        <button type="button" class="wz-link" onclick={checkKey}>Try again</button>
        {#if draft.type === 'ollama'}<span class="wz-hint">In Docker? Use http://host.docker.internal:11434</span>{/if}
        <button type="button" class="wz-link muted" onclick={() => { saveAnyway = true }} disabled={saveAnyway}>{saveAnyway ? 'Will save anyway' : 'Save anyway'}</button>
      </p>
    {:else if draft.probe?.status === 'error'}
      <p class="wz-status bad">{draft.probe.message}</p>
    {/if}
  </div>

  <div>
    <button type="button" class="wz-disclosure" aria-expanded={advancedOpen} aria-controls="wizard-advanced" onclick={() => { advancedOpen = !advancedOpen }}>
      <span class="wz-disclosure-title">
        <span class="chevron-toggle" class:open={advancedOpen} aria-hidden="true">
          <svg width="12" height="12" viewBox="0 0 12 12"><path d="M4.5 3l3 3-3 3" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
        </span>
        Advanced
      </span>
      {#if !advancedOpen}<span class="wz-disclosure-summary">{advancedSummary}</span>{/if}
    </button>
    {#if advancedOpen}
      <div id="wizard-advanced" class="advanced">
        <div class="wz-row">
          <div class="wz-field">
            <label class="wz-label" for="wizard-provider-name">Name</label>
            <input id="wizard-provider-name" class="wz-input" bind:value={draft.name} oninput={() => { draft.nameEdited = true }} disabled={saving || resumed} data-testid="wizard-provider-name" />
            <span class="wz-hint">How agents refer to this provider.</span>
          </div>
          {#if meta.needsKey}
            <div class="wz-field">
              <label class="wz-label" for="wizard-base-url">Base URL</label>
              <input id="wizard-base-url" class="wz-input mono" type="url" placeholder="Standard" bind:value={draft.baseURL} onblur={onKeyBlur} disabled={saving} data-testid="wizard-provider-baseurl" />
            </div>
          {/if}
        </div>
        <label class="toggle-row">
          <span class="switch switch-sm">
            <input type="checkbox" bind:checked={draft.setDefault} disabled={saving} />
            <span class="switch-slider"></span>
          </span>
          <span>Use as the default provider</span>
        </label>
      </div>
    {/if}
  </div>

  {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
</div>

<style>
  .probe { display: flex; flex-direction: column; gap: 6px; margin-top: -16px; }
  .probe-row { display: flex; justify-content: space-between; align-items: baseline; gap: 12px; }
  .probe-actions { display: flex; flex-wrap: wrap; align-items: baseline; gap: 16px; }
  .advanced { display: flex; flex-direction: column; gap: 16px; padding-top: 16px; }
  .toggle-row { display: flex; align-items: center; gap: 10px; font-size: 14px; cursor: pointer; }
  .spinner {
    width: 14px;
    height: 14px;
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
    flex-shrink: 0;
    margin-top: 2px;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .spinner { animation: none; } }
</style>
