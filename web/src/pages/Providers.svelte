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

  const providerLabels = {
    anthropic: 'Anthropic',
    openrouter: 'OpenRouter',
    openai: 'OpenAI',
    ollama: 'Ollama',
  }

  const providerDescriptions = {
    anthropic: 'Direct access to Claude models. Requires an API key from console.anthropic.com.',
    openrouter: 'Multi-model gateway with access to models from many providers.',
    openai: 'GPT models and OpenAI-compatible endpoints (Azure, vLLM, LiteLLM).',
    ollama: 'Local model inference. No API key required.',
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
      cost_limit_soft: data.cost_limit_soft || 0,
      cost_limit_hard: data.cost_limit_hard || 0,
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
      if (configDraft.cost_limit_soft !== (data.cost_limit_soft || 0)) patch.cost_limit_soft = parseFloat(configDraft.cost_limit_soft) || 0
      if (configDraft.cost_limit_hard !== (data.cost_limit_hard || 0)) patch.cost_limit_hard = parseFloat(configDraft.cost_limit_hard) || 0

      if (Object.keys(patch).length > 0) {
        await api.updateLLMConfig(patch)
        // Update local state
        if (patch.default_provider !== undefined) data.default_provider = patch.default_provider
        if (patch.default_model !== undefined) data.default_model = patch.default_model
        if (patch.cost_limit_soft !== undefined) data.cost_limit_soft = patch.cost_limit_soft
        if (patch.cost_limit_hard !== undefined) data.cost_limit_hard = patch.cost_limit_hard
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
      if (editingProvider === 'openai' && providerDraft.organization !== (p?.organization || '')) {
        patch.organization = providerDraft.organization
      }

      if (Object.keys(patch).length > 0) {
        await api.updateLLMProvider(editingProvider, patch)
        // Update local state
        if (p) {
          if (patch.api_key) p.api_key_set = true
          if (patch.api_key) p.enabled = true
          if (patch.base_url !== undefined) p.base_url = patch.base_url
          if (patch.organization !== undefined) p.organization = patch.organization
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

  function hasEditableFields(name) {
    return name !== 'openrouter'
  }

  onMount(fetchData)
</script>

<h1 class="page-title">Providers</h1>
<ErrorBanner message={error} />

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
        <div class="default-item">
          <span class="default-label">Cost Limit (soft)</span>
          <span class="default-value">{data.cost_limit_soft > 0 ? `$${data.cost_limit_soft.toFixed(2)}` : 'None'}</span>
        </div>
        <div class="default-item">
          <span class="default-label">Cost Limit (hard)</span>
          <span class="default-value">{data.cost_limit_hard > 0 ? `$${data.cost_limit_hard.toFixed(2)}` : 'None'}</span>
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
              <option value={p.name}>{providerLabels[p.name]}</option>
            {/each}
          </select>
        </div>
        <div class="form-row">
          <label class="form-label">Default Model</label>
          <ModelSelector bind:value={configDraft.default_model} provider={configDraft.default_provider} />
        </div>
        <div class="form-row-pair">
          <div class="form-row">
            <label class="form-label" for="cost-soft">Cost Limit Soft ($)</label>
            <input id="cost-soft" type="number" class="input" bind:value={configDraft.cost_limit_soft} min="0" step="0.01" />
          </div>
          <div class="form-row">
            <label class="form-label" for="cost-hard">Cost Limit Hard ($)</label>
            <input id="cost-hard" type="number" class="input" bind:value={configDraft.cost_limit_hard} min="0" step="0.01" />
          </div>
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
          <span class="provider-name">{providerLabels[p.name]}</span>
          <span class="provider-status" class:enabled={p.enabled} class:disabled={!p.enabled}>
            {p.enabled ? 'Enabled' : 'Not configured'}
          </span>
        </div>
        <div class="provider-desc">{providerDescriptions[p.name]}</div>
      </div>

      {#if editingProvider !== p.name}
        <div class="provider-fields">
          {#if p.name !== 'ollama'}
            <div class="field-row">
              <span class="field-label">API Key</span>
              <span class="field-value">{p.api_key_set ? 'Configured' : 'Not set'}</span>
            </div>
          {/if}
          {#if p.name === 'anthropic' || p.name === 'openai' || p.name === 'ollama'}
            <div class="field-row">
              <span class="field-label">Base URL</span>
              <span class="field-value mono">{p.base_url || '(default)'}</span>
            </div>
          {/if}
          {#if p.name === 'openai'}
            <div class="field-row">
              <span class="field-label">Organization</span>
              <span class="field-value mono">{p.organization || '(none)'}</span>
            </div>
          {/if}
        </div>
        <div class="card-actions">
          <button class="btn btn-sm" onclick={() => startEditProvider(p.name)}>Edit</button>
        </div>
      {:else}
        <div class="edit-form">
          {#if p.name !== 'ollama'}
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
          {#if hasEditableFields(p.name)}
            <div class="form-row">
              <label class="form-label" for="base-url-{p.name}">Base URL</label>
              <input
                id="base-url-{p.name}"
                type="url"
                class="input"
                bind:value={providerDraft.base_url}
                placeholder={p.name === 'ollama' ? 'http://localhost:11434' : 'https://api.example.com'}
              />
            </div>
          {/if}
          {#if p.name === 'openai'}
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
</style>
