<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ErrorBanner from '../components/ErrorBanner.svelte'
  import ModelSelector from '../components/ModelSelector.svelte'

  let data = $state(null)
  let loading = $state(true)
  let error = $state('')

  // LLM config editing
  let editingConfig = $state(false)
  let configDraft = $state({})
  let savingConfig = $state(false)
  let saveConfigOk = $state(false)

  // Per-provider editing: keyed by provider name
  let editingProvider = $state(null)
  let providerDraft = $state({})
  let savingProvider = $state(false)
  let saveProviderOk = $state(null)

  const typeLabels = {
    anthropic: 'Anthropic',
    openrouter: 'OpenRouter',
    openai: 'OpenAI',
    ollama: 'Ollama',
  }

  const typeDescriptions = {
    anthropic: 'Direct access to Claude models. Requires an API key from console.anthropic.com.',
    openrouter: 'Multi-model gateway with access to models from many providers.',
    openai: 'GPT models and OpenAI-compatible endpoints (Azure, vLLM, LiteLLM).',
    ollama: 'Local model inference. No API key required.',
  }

  function providerLabel(p) {
    const tl = typeLabels[p.type] || p.type
    return p.name === p.type ? tl : `${p.name} (${tl})`
  }

  function providerDescription(p) {
    return typeDescriptions[p.type] || ''
  }

  async function fetchData() {
    loading = true
    error = ''
    try {
      data = await api.llmProviders()
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  // --- LLM config editing ---

  function startEditConfig() {
    configDraft = {
      default_provider: data.default_provider || '',
      default_model: data.default_model || '',
    }
    editingConfig = true
  }

  function cancelEditConfig() {
    editingConfig = false
  }

  async function saveConfig() {
    savingConfig = true
    error = ''
    try {
      const patch = {}
      if (configDraft.default_provider !== (data.default_provider || '')) patch.default_provider = configDraft.default_provider
      if (configDraft.default_model !== (data.default_model || '')) patch.default_model = configDraft.default_model

      if (Object.keys(patch).length > 0) {
        await api.updateLLMConfig(patch)
        if (patch.default_provider !== undefined) data.default_provider = patch.default_provider
        if (patch.default_model !== undefined) data.default_model = patch.default_model
      }
      editingConfig = false
      saveConfigOk = true
      setTimeout(() => { saveConfigOk = false }, 3000)
    } catch (e) {
      error = e.message
    } finally {
      savingConfig = false
    }
  }

  // --- Provider editing ---

  function startEditProvider(name) {
    const p = data.providers.find(x => x.name === name)
    providerDraft = {
      api_key: '',
      base_url: p?.base_url || '',
      organization: p?.organization || '',
      reasoning_enabled: !!(p?.reasoning?.enabled),
      reasoning_effort: p?.reasoning?.effort || '',
      provider_sticky: p?.routing?.sticky ?? true,
      provider_sticky_ttl: p?.routing?.sticky_ttl || '',
      cost_limit_soft: p?.cost_limit_soft ?? '',
      cost_limit_hard: p?.cost_limit_hard ?? '',
      default_rate_per_1k_tokens: p?.default_rate_per_1k_tokens ?? '',
      model_prices: p?.model_prices
        ? Object.entries(p.model_prices).map(([k, v]) => ({ model: k, input: v.input, output: v.output, cached_input: v.cached_input }))
        : [],
    }
    editingProvider = name
  }

  function cancelEditProvider() {
    editingProvider = null
  }

  async function saveProvider() {
    savingProvider = true
    error = ''
    try {
      const p = data.providers.find(x => x.name === editingProvider)
      const patch = {}

      if (providerDraft.api_key) {
        patch.api_key = providerDraft.api_key
      }
      if (providerDraft.base_url !== (p?.base_url || '')) {
        patch.base_url = providerDraft.base_url
      }
      if (p?.type === 'openai' && providerDraft.organization !== (p?.organization || '')) {
        patch.organization = providerDraft.organization
      }

      // Build reasoning patch for OpenRouter.
      if (p?.type === 'openrouter') {
        const oldEnabled = !!(p?.reasoning?.enabled)
        const oldEffort = p?.reasoning?.effort || ''
        if (providerDraft.reasoning_enabled !== oldEnabled || providerDraft.reasoning_effort !== oldEffort) {
          patch.reasoning = {
            enabled: providerDraft.reasoning_enabled || undefined,
            effort: providerDraft.reasoning_effort || undefined,
          }
        }

        const oldSticky = p?.routing?.sticky ?? true
        const oldTtl = p?.routing?.sticky_ttl || ''
        if (providerDraft.provider_sticky !== oldSticky || providerDraft.provider_sticky_ttl !== oldTtl) {
          // Preserve any advanced order/fallback settings configured via TOML.
          patch.routing = {
            sticky: providerDraft.provider_sticky,
            sticky_ttl: providerDraft.provider_sticky_ttl || undefined,
            order: p?.routing?.order,
            allow_fallbacks: p?.routing?.allow_fallbacks,
          }
        }
      }

      // Cost fields.
      const softVal = providerDraft.cost_limit_soft !== '' ? parseFloat(providerDraft.cost_limit_soft) : null
      const hardVal = providerDraft.cost_limit_hard !== '' ? parseFloat(providerDraft.cost_limit_hard) : null
      const rateVal = providerDraft.default_rate_per_1k_tokens !== '' ? parseFloat(providerDraft.default_rate_per_1k_tokens) : null
      if (softVal !== (p?.cost_limit_soft ?? null)) patch.cost_limit_soft = softVal
      if (hardVal !== (p?.cost_limit_hard ?? null)) patch.cost_limit_hard = hardVal
      if (rateVal !== (p?.default_rate_per_1k_tokens ?? null)) patch.default_rate_per_1k_tokens = rateVal

      // Model price overrides — always send the full map when editing.
      const validPrices = providerDraft.model_prices.filter(r => r.model.trim())
      const oldCount = p?.model_prices ? Object.keys(p.model_prices).length : 0
      if (validPrices.length > 0 || oldCount > 0) {
        const mp = {}
        for (const r of validPrices) {
          mp[r.model.trim()] = {
            input: parseFloat(r.input) || 0,
            output: parseFloat(r.output) || 0,
            cached_input: parseFloat(r.cached_input) || 0,
          }
        }
        patch.model_prices = mp
      }

      if (Object.keys(patch).length > 0) {
        await api.updateLLMProvider(editingProvider, patch)
        if (p) {
          if (patch.api_key) p.api_key_set = true
          if (patch.api_key) p.enabled = true
          if (patch.base_url !== undefined) p.base_url = patch.base_url
          if (patch.organization !== undefined) p.organization = patch.organization
          if (patch.reasoning) p.reasoning = patch.reasoning
          if (patch.routing) p.routing = patch.routing
          if (patch.cost_limit_soft !== undefined) p.cost_limit_soft = patch.cost_limit_soft
          if (patch.cost_limit_hard !== undefined) p.cost_limit_hard = patch.cost_limit_hard
          if (patch.default_rate_per_1k_tokens !== undefined) p.default_rate_per_1k_tokens = patch.default_rate_per_1k_tokens
          if (patch.model_prices !== undefined) p.model_prices = Object.keys(patch.model_prices).length > 0 ? patch.model_prices : null
        }
      }

      const savedName = editingProvider
      editingProvider = null
      saveProviderOk = savedName
      setTimeout(() => { saveProviderOk = null }, 3000)
    } catch (e) {
      error = e.message
    } finally {
      savingProvider = false
    }
  }

  function hasEditableBaseURL(p) {
    return p.type !== 'openrouter'
  }

  // ---------------------------------------------------------------------------
  // Provider create
  // ---------------------------------------------------------------------------
  let showAddForm = $state(false)
  let formName = $state('')
  let formType = $state('openai')
  let formAPIKey = $state('')
  let formBaseURL = $state('')
  let formOrganization = $state('')
  let formSaving = $state(false)
  let formError = $state('')

  function openAddForm() {
    formName = ''
    formType = 'openai'
    formAPIKey = ''
    formBaseURL = ''
    formOrganization = ''
    formError = ''
    showAddForm = true
  }

  function closeAddForm() {
    showAddForm = false
    formError = ''
  }

  async function saveNewProvider() {
    formError = ''
    const name = formName.trim()
    if (!name) { formError = 'Name is required'; return }
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(name)) {
      formError = 'Name must be lowercase alphanumeric with hyphens only'
      return
    }
    formSaving = true
    try {
      const body = { name, type: formType }
      if (formAPIKey) body.api_key = formAPIKey
      if (formBaseURL) body.base_url = formBaseURL
      if (formOrganization && formType === 'openai') body.organization = formOrganization
      await api.createLLMProvider(body)
      data = await api.llmProviders()
      showAddForm = false
    } catch (e) {
      formError = e.message
    } finally {
      formSaving = false
    }
  }

  // ---------------------------------------------------------------------------
  // Provider delete
  // ---------------------------------------------------------------------------
  let confirmDelete = $state(null)
  let deleting = $state(false)
  let deleteError = $state('')

  async function performDelete(name) {
    deleting = true
    deleteError = ''
    try {
      await api.deleteLLMProvider(name)
      data = await api.llmProviders()
      confirmDelete = null
    } catch (e) {
      deleteError = e.message
    } finally {
      deleting = false
    }
  }

  onMount(fetchData)
</script>

<div class="page-header">
  <h1 class="page-title">Providers</h1>
  <button class="btn btn-sm btn-primary" onclick={openAddForm} data-testid="add-provider-btn">+ Add Provider</button>
</div>
<ErrorBanner message={error} />

{#if showAddForm}
<div class="inline-panel" data-testid="provider-form">
  <h3 class="form-title">Add Provider</h3>
  {#if formError}
    <div class="inline-error" role="alert">{formError}</div>
  {/if}
  <div class="form-row">
    <label class="form-label" for="new-provider-name">Name</label>
    <input id="new-provider-name" type="text" class="input" bind:value={formName} disabled={formSaving} placeholder="e.g. my-openai" data-testid="provider-name-input" />
  </div>
  <div class="form-row">
    <label class="form-label" for="new-provider-type">Type</label>
    <select id="new-provider-type" class="input" bind:value={formType} disabled={formSaving} data-testid="provider-type-select">
      {#each Object.entries(typeLabels) as [value, label]}
        <option {value}>{label}</option>
      {/each}
    </select>
  </div>
  {#if formType !== 'ollama'}
    <div class="form-row">
      <label class="form-label" for="new-provider-apikey">API Key</label>
      <input id="new-provider-apikey" type="password" class="input" bind:value={formAPIKey} disabled={formSaving} placeholder="Enter API key" />
    </div>
  {/if}
  {#if formType !== 'openrouter'}
    <div class="form-row">
      <label class="form-label" for="new-provider-baseurl">Base URL</label>
      <input id="new-provider-baseurl" type="url" class="input" bind:value={formBaseURL} disabled={formSaving} placeholder={formType === 'ollama' ? 'http://localhost:11434' : 'https://api.example.com'} />
    </div>
  {/if}
  {#if formType === 'openai'}
    <div class="form-row">
      <label class="form-label" for="new-provider-org">Organization</label>
      <input id="new-provider-org" type="text" class="input" bind:value={formOrganization} disabled={formSaving} placeholder="org-..." />
    </div>
  {/if}
  <div class="restart-note">New providers require a restart to take effect.</div>
  <div class="config-actions">
    <button class="btn btn-primary" onclick={saveNewProvider} disabled={formSaving || !formName.trim()} data-testid="provider-save-btn">
      {formSaving ? 'Creating\u2026' : 'Create'}
    </button>
    <button class="btn" onclick={closeAddForm} disabled={formSaving}>Cancel</button>
  </div>
</div>
{/if}

{#if loading && !data}
  <p class="loading">Loading...</p>
{:else if data}
  <h2 class="section-title">LLM Defaults</h2>
  <div class="config-card">
    {#if !editingConfig}
      <div class="defaults-grid">
        <div class="default-item">
          <span class="default-label">Default Provider</span>
          <span class="default-value mono">{data.default_provider || '(not set)'}</span>
        </div>
        <div class="default-item">
          <span class="default-label">Default Model</span>
          <span class="default-value mono">{data.default_model || '(not set)'}</span>
        </div>
      </div>
      <div class="card-actions">
        <button class="btn btn-sm" onclick={startEditConfig}>Edit</button>
      </div>
    {:else}
      <div class="edit-form">
        <div class="form-row">
          <label class="form-label" for="default-provider">Default Provider</label>
          <select id="default-provider" class="input" bind:value={configDraft.default_provider}>
            <option value="">— none —</option>
            {#each data.providers as p}
              <option value={p.name}>{providerLabel(p)}</option>
            {/each}
          </select>
        </div>
        <div class="form-row">
          <label class="form-label">Default Model</label>
          <ModelSelector bind:value={configDraft.default_model} provider={configDraft.default_provider} />
        </div>
        <div class="config-actions">
          <button class="btn btn-primary" onclick={saveConfig} disabled={savingConfig}>
            {savingConfig ? 'Saving...' : 'Save'}
          </button>
          <button class="btn" onclick={cancelEditConfig} disabled={savingConfig}>Cancel</button>
        </div>
      </div>
    {/if}
    {#if saveConfigOk}
      <div class="save-ok">Saved</div>
    {/if}
  </div>

  <h2 class="section-title">Configured Providers</h2>
  {#each data.providers as p (p.name)}
    <div class="config-card provider-card">
      <div class="provider-header">
        <div class="provider-title">
          <span class="provider-name">{providerLabel(p)}</span>
          <span class="provider-status" class:enabled={p.enabled} class:disabled={!p.enabled}>
            {p.enabled ? 'Enabled' : 'Not configured'}
          </span>
        </div>
        <div class="provider-desc">{providerDescription(p)}</div>
      </div>

      {#if editingProvider !== p.name}
        <div class="provider-fields">
          {#if p.type !== 'ollama'}
            <div class="field-row">
              <span class="field-label">API Key</span>
              <span class="field-value">{p.api_key_set ? 'Configured' : 'Not set'}</span>
            </div>
          {/if}
          {#if p.type !== 'openrouter'}
            <div class="field-row">
              <span class="field-label">Base URL</span>
              <span class="field-value mono">{p.base_url || '(default)'}</span>
            </div>
          {/if}
          {#if p.type === 'openai'}
            <div class="field-row">
              <span class="field-label">Organization</span>
              <span class="field-value mono">{p.organization || '(none)'}</span>
            </div>
          {/if}
          {#if p.type === 'openrouter'}
            <div class="field-row">
              <span class="field-label">Reasoning</span>
              <span class="field-value">
                {#if p.reasoning?.enabled}
                  Enabled{#if p.reasoning.effort} · {p.reasoning.effort} effort{/if}
                {:else}
                  Off
                {/if}
              </span>
            </div>
            <div class="field-row">
              <span class="field-label">Caching</span>
              <span class="field-value">
                {#if (p.routing?.sticky ?? true)}
                  Sticky{#if p.routing?.sticky_ttl} · {p.routing.sticky_ttl}{/if}
                {:else}
                  Off
                {/if}
              </span>
            </div>
          {/if}
        </div>
        {#if p.cost_limit_soft != null || p.cost_limit_hard != null || p.default_rate_per_1k_tokens != null || (p.model_prices && Object.keys(p.model_prices).length > 0)}
          <div class="provider-fields costs-section">
            {#if p.cost_limit_soft != null}
              <div class="field-row">
                <span class="field-label">Soft Limit</span>
                <span class="field-value">${p.cost_limit_soft.toFixed(2)}</span>
              </div>
            {/if}
            {#if p.cost_limit_hard != null}
              <div class="field-row">
                <span class="field-label">Hard Limit</span>
                <span class="field-value">${p.cost_limit_hard.toFixed(2)}</span>
              </div>
            {/if}
            {#if p.default_rate_per_1k_tokens != null}
              <div class="field-row">
                <span class="field-label">Fallback Rate</span>
                <span class="field-value">${p.default_rate_per_1k_tokens}/1k tokens</span>
              </div>
            {/if}
            {#if p.model_prices && Object.keys(p.model_prices).length > 0}
              <div class="field-row">
                <span class="field-label">Model Overrides</span>
                <span class="field-value">{Object.keys(p.model_prices).length} model{Object.keys(p.model_prices).length !== 1 ? 's' : ''}</span>
              </div>
            {/if}
          </div>
        {/if}
        <div class="card-actions">
          <button class="btn btn-sm" onclick={() => startEditProvider(p.name)}>Edit</button>
          <button class="btn btn-sm btn-danger-text" onclick={() => { confirmDelete = p.name; deleteError = '' }} data-testid="delete-provider-btn">Delete</button>
        </div>
        {#if confirmDelete === p.name}
          <div class="delete-confirm" data-testid="delete-confirm">
            <span>Delete provider <strong>{p.name}</strong>?</span>
            {#if deleteError}
              <div class="inline-error" role="alert">{deleteError}</div>
            {/if}
            <div class="delete-actions">
              <button class="btn btn-primary btn-danger" onclick={() => performDelete(p.name)} disabled={deleting} data-testid="delete-confirm-btn">
                {deleting ? 'Deleting\u2026' : 'Delete'}
              </button>
              <button class="btn" onclick={() => { confirmDelete = null; deleteError = '' }} disabled={deleting}>Cancel</button>
            </div>
          </div>
        {/if}
      {:else}
        <div class="edit-form">
          {#if p.type !== 'ollama'}
            <div class="form-row">
              <label class="form-label" for="api-key-{p.name}">API Key</label>
              <input
                id="api-key-{p.name}"
                type="password"
                class="input"
                bind:value={providerDraft.api_key}
                placeholder={p.api_key_set ? '(leave blank to keep current)' : 'Enter API key'}
              />
            </div>
          {/if}
          {#if hasEditableBaseURL(p)}
            <div class="form-row">
              <label class="form-label" for="base-url-{p.name}">Base URL</label>
              <input
                id="base-url-{p.name}"
                type="url"
                class="input"
                bind:value={providerDraft.base_url}
                placeholder={p.type === 'ollama' ? 'http://localhost:11434' : 'https://api.example.com'}
              />
            </div>
          {/if}
          {#if p.type === 'openai'}
            <div class="form-row">
              <label class="form-label" for="org-{p.name}">Organization</label>
              <input
                id="org-{p.name}"
                type="text"
                class="input"
                bind:value={providerDraft.organization}
                placeholder="org-..."
              />
            </div>
          {/if}
          {#if p.type === 'openrouter'}
            <div class="form-row">
              <label class="form-label">Reasoning</label>
              <div class="reasoning-controls">
                <label class="toggle-label">
                  <input type="checkbox" bind:checked={providerDraft.reasoning_enabled} />
                  Enable reasoning
                </label>
                {#if providerDraft.reasoning_enabled}
                  <div class="reasoning-effort">
                    <label class="form-label-sm" for="reasoning-effort-{p.name}">Effort</label>
                    <select id="reasoning-effort-{p.name}" class="input input-sm" bind:value={providerDraft.reasoning_effort}>
                      <option value="">Default</option>
                      <option value="xhigh">Extra high</option>
                      <option value="high">High</option>
                      <option value="medium">Medium</option>
                      <option value="low">Low</option>
                      <option value="minimal">Minimal</option>
                    </select>
                  </div>
                {/if}
              </div>
            </div>
            <div class="form-row">
              <label class="form-label">Provider caching</label>
              <div class="reasoning-controls">
                <label class="toggle-label">
                  <input type="checkbox" bind:checked={providerDraft.provider_sticky} />
                  Sticky provider routing
                </label>
                <p class="hint">Prefer the upstream provider that last served a response, so its automatic prompt cache keeps hitting. Resets on any error.</p>
                {#if providerDraft.provider_sticky}
                  <div class="reasoning-effort">
                    <label class="form-label-sm" for="sticky-ttl-{p.name}">Window</label>
                    <input id="sticky-ttl-{p.name}" type="text" class="input input-sm" bind:value={providerDraft.provider_sticky_ttl} placeholder="1h" />
                  </div>
                {/if}
              </div>
            </div>
          {/if}
          <div class="cost-section-header">Cost Limits</div>
          <div class="form-row-pair">
            <div class="form-row">
              <label class="form-label" for="cost-soft-{p.name}">Soft Limit ($)</label>
              <input id="cost-soft-{p.name}" type="number" class="input" bind:value={providerDraft.cost_limit_soft} min="0" step="0.01" placeholder="None" />
            </div>
            <div class="form-row">
              <label class="form-label" for="cost-hard-{p.name}">Hard Limit ($)</label>
              <input id="cost-hard-{p.name}" type="number" class="input" bind:value={providerDraft.cost_limit_hard} min="0" step="0.01" placeholder="None" />
            </div>
          </div>
          <div class="form-row">
            <label class="form-label" for="fallback-rate-{p.name}">Fallback Rate ($/1K tokens)</label>
            <input id="fallback-rate-{p.name}" type="number" class="input" bind:value={providerDraft.default_rate_per_1k_tokens} min="0" step="0.001" placeholder="None" />
          </div>
          <div class="model-prices-section">
            <div class="form-label">Model Price Overrides</div>
            {#if providerDraft.model_prices.length > 0}
              <div class="model-prices-table">
                <div class="model-prices-header">
                  <span>Model</span>
                  <span>Input $/M</span>
                  <span>Output $/M</span>
                  <span>Cached $/M</span>
                  <span></span>
                </div>
                {#each providerDraft.model_prices as row, i}
                  <div class="model-prices-row">
                    <input type="text" class="input input-sm" bind:value={row.model} placeholder="model-name" />
                    <input type="number" class="input input-sm" bind:value={row.input} min="0" step="0.01" placeholder="0" />
                    <input type="number" class="input input-sm" bind:value={row.output} min="0" step="0.01" placeholder="0" />
                    <input type="number" class="input input-sm" bind:value={row.cached_input} min="0" step="0.01" placeholder="0" />
                    <button class="btn btn-sm btn-danger-text" onclick={() => { providerDraft.model_prices = providerDraft.model_prices.filter((_, idx) => idx !== i) }}>Remove</button>
                  </div>
                {/each}
              </div>
            {/if}
            <button class="btn btn-sm" onclick={() => { providerDraft.model_prices = [...providerDraft.model_prices, { model: '', input: '', output: '', cached_input: '' }] }}>Add Override</button>
          </div>
          <div class="restart-note">Changes to provider settings require a restart to take effect.</div>
          <div class="config-actions">
            <button class="btn btn-primary" onclick={saveProvider} disabled={savingProvider}>
              {savingProvider ? 'Saving...' : 'Save'}
            </button>
            <button class="btn" onclick={cancelEditProvider} disabled={savingProvider}>Cancel</button>
          </div>
        </div>
      {/if}
      {#if saveProviderOk === p.name}
        <div class="save-ok">Saved — restart to apply</div>
      {/if}
    </div>
  {/each}
{/if}

<style>
  .page-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 20px;
  }
  .page-header .page-title { margin-bottom: 0; }
  .page-title { font-size: 20px; font-weight: 700; margin-bottom: 20px; }
  .section-title { font-size: 16px; font-weight: 600; margin: 28px 0 12px; }
  .section-title:first-of-type { margin-top: 0; }
  .loading { color: var(--text-muted); }
  .mono { font-family: monospace; }

  .config-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 20px;
  }

  .provider-card {
    margin-bottom: 14px;
  }

  .provider-header {
    margin-bottom: 14px;
  }

  .provider-title {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .provider-name {
    font-weight: 600;
    font-size: 14px;
  }

  .provider-status {
    font-size: 11px;
    padding: 2px 8px;
    border-radius: 10px;
    font-weight: 500;
  }
  .provider-status.enabled {
    background: color-mix(in srgb, var(--success) 15%, transparent);
    color: var(--success);
  }
  .provider-status.disabled {
    background: color-mix(in srgb, var(--text-muted) 15%, transparent);
    color: var(--text-muted);
  }

  .provider-desc {
    font-size: 12px;
    color: var(--text-muted);
    margin-top: 4px;
    line-height: 1.4;
  }

  .provider-fields {
    margin-bottom: 10px;
  }

  .field-row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 6px 0;
  }
  .field-row + .field-row {
    border-top: 1px solid var(--border);
  }
  .field-label {
    font-size: 12px;
    color: var(--text-muted);
    min-width: 90px;
    flex-shrink: 0;
  }
  .field-value {
    font-size: 13px;
  }

  .card-actions {
    display: flex;
    justify-content: flex-end;
    margin-top: 6px;
  }

  /* Defaults grid */
  .defaults-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 14px;
    margin-bottom: 6px;
  }
  .default-item {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .default-label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .default-value {
    font-size: 13px;
  }

  /* Edit form */
  .edit-form {
    margin-top: 10px;
  }
  .form-row {
    margin-bottom: 10px;
  }
  .form-label {
    display: block;
    font-size: 12px;
    color: var(--text-muted);
    margin-bottom: 4px;
  }
  .form-row-pair {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 14px;
  }

  .input {
    width: 100%;
    padding: 8px 10px;
    font-size: 13px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--text);
  }
  .input:focus {
    outline: none;
    border-color: var(--accent);
  }
  select.input {
    cursor: pointer;
  }

  .reasoning-controls {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .reasoning-controls .hint {
    margin: 0;
  }
  .toggle-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
  }
  .toggle-label input[type="checkbox"] {
    cursor: pointer;
  }
  .reasoning-effort {
    display: flex;
    align-items: center;
    gap: 8px;
    padding-left: 22px;
  }
  .form-label-sm {
    font-size: 12px;
    color: var(--text-muted);
    flex-shrink: 0;
  }
  .input-sm {
    width: auto;
    padding: 4px 8px;
    font-size: 12px;
  }

  .config-actions {
    margin-top: 10px;
    display: flex;
    gap: 8px;
  }

  .restart-note {
    font-size: 12px;
    color: var(--text-muted);
    margin-top: 8px;
    font-style: italic;
  }

  .btn {
    padding: 6px 14px;
    font-size: 13px;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    cursor: pointer;
    transition: border-color 0.2s, color 0.2s;
  }
  .btn:hover { border-color: var(--text-muted); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .btn-sm { padding: 4px 10px; font-size: 12px; }
  .btn-primary {
    background: var(--accent);
    color: #fff;
    border-color: var(--accent);
  }
  .btn-primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); }

  .save-ok {
    margin-top: 8px;
    font-size: 12px;
    color: var(--success);
    font-weight: 500;
  }

  /* Inline add form */
  .inline-panel {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 20px;
    margin-bottom: 14px;
  }
  .form-title {
    font-size: 14px;
    font-weight: 600;
    margin-bottom: 12px;
  }
  .inline-error {
    color: var(--danger, #d32f2f);
    font-size: 12px;
    margin-bottom: 8px;
  }

  /* Delete */
  .btn-danger-text {
    color: var(--danger, #d32f2f);
    border-color: transparent;
  }
  .btn-danger-text:hover {
    border-color: var(--danger, #d32f2f);
  }
  .btn-danger {
    background: var(--danger, #d32f2f);
    border-color: var(--danger, #d32f2f);
    color: #fff;
  }
  .btn-danger:hover {
    opacity: 0.9;
  }
  .delete-confirm {
    margin-top: 10px;
    padding: 10px;
    background: color-mix(in srgb, var(--danger, #d32f2f) 5%, transparent);
    border: 1px solid color-mix(in srgb, var(--danger, #d32f2f) 20%, transparent);
    border-radius: var(--radius);
    font-size: 13px;
  }
  .delete-actions {
    display: flex;
    gap: 8px;
    margin-top: 8px;
  }

  .costs-section {
    border-top: 1px solid var(--border);
    padding-top: 8px;
    margin-top: 8px;
  }

  .cost-section-header {
    font-size: 12px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-top: 14px;
    margin-bottom: 8px;
    padding-top: 10px;
    border-top: 1px solid var(--border);
  }

  .model-prices-section {
    margin-top: 10px;
  }
  .model-prices-table {
    margin-bottom: 8px;
  }
  .model-prices-header,
  .model-prices-row {
    display: grid;
    grid-template-columns: 2fr 1fr 1fr 1fr auto;
    gap: 6px;
    align-items: center;
  }
  .model-prices-header {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.03em;
    padding-bottom: 4px;
    border-bottom: 1px solid var(--border);
  }
  .model-prices-row {
    padding: 4px 0;
  }
  .model-prices-row + .model-prices-row {
    border-top: 1px solid var(--border);
  }
</style>
