<script>
  import { api } from '../api.js'

  let { value = $bindable(''), onchange, provider = '' } = $props()

  let models = $state([])
  let search = $state('')
  let toolsOnly = $state(true)
  let sortBy = $state('popularity') // 'name' | 'price' | 'popularity'
  let providerFilter = $state('') // internal filter when provider prop is not set
  let open = $state(false)
  let loading = $state(false)
  let inputEl = $state(null)
  let containerEl = $state(null)
  let loadedProvider = $state(null) // tracks which provider filter was used for the last fetch

  // Known provider names from the lightweight /llm/providers endpoint.
  let knownProviders = $state([])
  let providersLoaded = $state(false)

  // Effective provider: external prop takes precedence over internal filter.
  let activeProvider = $derived(provider || providerFilter)

  let filtered = $derived(() => {
    let list = models
    if (toolsOnly) list = list.filter(m => m.supports_tools)
    if (search) {
      const q = search.toLowerCase()
      list = list.filter(m => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q))
    }
    // Client-side sort
    list = [...list]
    if (sortBy === 'name') {
      list.sort((a, b) => a.name.localeCompare(b.name))
    } else if (sortBy === 'price') {
      list.sort((a, b) => (a.input_per_mtok ?? Infinity) - (b.input_per_mtok ?? Infinity))
    } else {
      // popularity — already sorted by server, but re-sort in case of filter
      list.sort((a, b) => (b.weekly_tokens || 0) - (a.weekly_tokens || 0))
    }
    return list
  })

  // Fetch the lightweight provider list for the dropdown (no external API calls).
  async function loadProviders() {
    if (providersLoaded) return
    try {
      const data = await api.llmProviders()
      knownProviders = (data.providers || []).filter(p => p.enabled).map(p => p.name)
    } catch {
      knownProviders = []
    }
    providersLoaded = true
  }

  async function loadModels(forProvider) {
    if (loading) return
    // Skip if we already loaded for this exact provider filter.
    if (models.length && loadedProvider === (forProvider || '')) return
    loading = true
    models = await api.modelDetails(forProvider || '')
    loadedProvider = forProvider || ''
    loading = false
  }

  function handleFocus() {
    loadProviders()
    // When provider prop is set, or user already picked one, load models immediately.
    // Otherwise wait for them to pick a provider first.
    if (activeProvider) {
      loadModels(activeProvider)
    }
    open = true
    search = ''
  }

  function onProviderChange(e) {
    providerFilter = e.target.value
    if (e.target.value) {
      loadModels(e.target.value)
    } else {
      // "All providers" — fetch everything.
      loadModels('')
    }
  }

  function handleBlur(e) {
    if (e.relatedTarget && containerEl?.contains(e.relatedTarget)) return
    setTimeout(() => { open = false }, 180)
  }

  function select(m) {
    value = m.id
    open = false
    search = ''
    onchange?.(m.id)
  }

  function formatCost(v) {
    if (v == null) return '—'
    if (v === 0) return 'Free'
    if (v < 0.01) return '<$0.01'
    return '$' + v.toFixed(2)
  }

  function formatTokens(v) {
    if (!v) return ''
    if (v >= 1_000_000_000) return (v / 1_000_000_000).toFixed(1) + 'B/wk'
    if (v >= 1_000_000) return (v / 1_000_000).toFixed(1) + 'M/wk'
    if (v >= 1_000) return (v / 1_000).toFixed(1) + 'K/wk'
    return v + '/wk'
  }

  // Precompute sorted token values for percentile lookups.
  let tokenRanks = $derived(() => {
    const vals = models.map(m => m.weekly_tokens || 0).filter(v => v > 0)
    vals.sort((a, b) => a - b)
    return vals
  })

  // Returns 1-5 bars based on percentile rank within the dataset.
  function popularityBars(v) {
    if (!v) return 0
    const ranks = tokenRanks()
    if (!ranks.length) return 0
    // Find percentile position via bisect
    let lo = 0, hi = ranks.length
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (ranks[mid] < v) lo = mid + 1
      else hi = mid
    }
    const pct = lo / ranks.length
    if (pct >= 0.80) return 5
    if (pct >= 0.60) return 4
    if (pct >= 0.40) return 3
    if (pct >= 0.20) return 2
    return 1
  }
</script>

<div class="model-selector" bind:this={containerEl} onfocusout={handleBlur}>
  <input
    bind:this={inputEl}
    class="selector-input mono"
    type="text"
    bind:value={value}
    placeholder="e.g. anthropic/claude-sonnet-4-20250514"
    onfocus={handleFocus}
    autocomplete="off"
  />

  {#if open}
    <div class="model-dropdown">
      <div class="model-toolbar">
        <input
          class="model-search"
          type="text"
          bind:value={search}
          placeholder="Filter models…"
          autocomplete="off"
        />
        {#if !provider}
          <select
            class="provider-select"
            value={providerFilter}
            onchange={onProviderChange}
            onmousedown={(e) => e.stopPropagation()}
          >
            <option value="" disabled={!providerFilter}>Provider{providerFilter ? ': All' : ''}</option>
            {#each knownProviders as p}
              <option value={p}>{p}</option>
            {/each}
          </select>
        {/if}
        <label class="model-filter">
          <input type="checkbox" bind:checked={toolsOnly} />
          Tools
        </label>
      </div>

      <div class="model-sortbar">
        <span class="sort-label">Sort by:</span>
        <button class="sort-btn" class:active={sortBy === 'name'} onmousedown={(e) => { e.preventDefault(); sortBy = 'name' }}>Name</button>
        <button class="sort-btn" class:active={sortBy === 'price'} onmousedown={(e) => { e.preventDefault(); sortBy = 'price' }}>Price</button>
        <button class="sort-btn" class:active={sortBy === 'popularity'} onmousedown={(e) => { e.preventDefault(); sortBy = 'popularity' }}>Popularity</button>
      </div>

      {#if loading}
        <div class="model-empty">Loading models…</div>
      {:else if !activeProvider && !models.length}
        <div class="model-empty">Select a provider to browse models, or type a model ID above</div>
      {:else if !filtered().length}
        <div class="model-empty">No models found</div>
      {:else}
        <div class="model-list">
          {#each filtered() as m}
            <button
              class="model-option"
              class:selected={m.id === value}
              onmousedown={(e) => { e.preventDefault(); select(m) }}
            >
              <div class="model-option-left">
                <span class="model-option-name">{m.name}</span>
                <span class="model-option-id mono">{m.id}</span>
              </div>
              <div class="model-option-right">
                {#if m.weekly_tokens}
                  <span class="model-popularity">
                    <span class="pop-count">{formatTokens(m.weekly_tokens)}</span>
                    <span class="pop-bars">
                      {#each Array(5) as _, i}
                        <span class="pop-bar" class:filled={i < popularityBars(m.weekly_tokens)}></span>
                      {/each}
                    </span>
                  </span>
                {/if}
                <span class="model-option-provider">{m.provider}</span>
                <span class="model-option-cost">{formatCost(m.input_per_mtok)}/{formatCost(m.output_per_mtok)} per M</span>
                {#if m.input_per_mtok === 0 && m.output_per_mtok === 0}
                  <span class="model-free-tag">FREE</span>
                {/if}
                {#if m.supports_tools}
                  <span class="model-tools-tag">TOOLS</span>
                {/if}
              </div>
            </button>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .model-selector {
    position: relative;
    width: 100%;
  }

  .selector-input {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 6px 10px;
    font-size: 12px;
    width: 100%;
    box-sizing: border-box;
  }
  .selector-input:focus { outline: none; border-color: var(--accent); }

  .model-dropdown {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    right: 0;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    z-index: 100;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
    max-height: 400px;
    display: flex;
    flex-direction: column;
  }

  .model-toolbar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .model-search {
    flex: 1;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 4px 8px;
    font-size: 12px;
    outline: none;
  }
  .model-search:focus { border-color: var(--accent); }

  .provider-select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 4px 6px;
    font-size: 11px;
    cursor: pointer;
    outline: none;
  }
  .provider-select:focus { border-color: var(--accent); }

  .model-filter {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 11px;
    color: var(--text-muted);
    cursor: pointer;
    white-space: nowrap;
    user-select: none;
  }
  .model-filter input { margin: 0; cursor: pointer; }

  /* Sort bar */
  .model-sortbar {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 6px 8px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .sort-label {
    font-size: 11px;
    color: var(--text-muted);
    margin-right: 2px;
  }

  .sort-btn {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    color: var(--text);
    padding: 2px 10px;
    font-size: 11px;
    cursor: pointer;
    transition: background 0.1s, color 0.1s;
  }
  .sort-btn:hover { border-color: var(--accent); }
  .sort-btn.active {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--bg);
    font-weight: 600;
  }

  .model-list {
    overflow-y: auto;
    flex: 1;
  }

  .model-option {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 8px 10px;
    background: none;
    border: none;
    border-bottom: 1px solid var(--border);
    cursor: pointer;
    text-align: left;
    color: var(--text);
    font-size: 12px;
  }
  .model-option:last-child { border-bottom: none; }
  .model-option:hover { background: var(--bg); }
  .model-option.selected { background: var(--bg); border-left: 2px solid var(--accent); }

  .model-option-left {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
    flex: 1;
  }

  .model-option-name {
    font-weight: 600;
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .model-option-id {
    font-size: 10px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .model-option-right {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
    font-size: 10px;
    color: var(--text-muted);
  }

  /* Popularity display */
  .model-popularity {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .pop-count {
    font-size: 10px;
    white-space: nowrap;
  }

  .pop-bars {
    display: flex;
    align-items: flex-end;
    gap: 1px;
    height: 12px;
  }

  .pop-bar {
    width: 3px;
    background: var(--border);
    border-radius: 1px;
  }
  .pop-bar:nth-child(1) { height: 4px; }
  .pop-bar:nth-child(2) { height: 6px; }
  .pop-bar:nth-child(3) { height: 8px; }
  .pop-bar:nth-child(4) { height: 10px; }
  .pop-bar:nth-child(5) { height: 12px; }
  .pop-bar.filled { background: var(--accent); }

  .model-option-provider {
    white-space: nowrap;
  }

  .model-option-cost {
    white-space: nowrap;
  }

  .model-tools-tag, .model-free-tag {
    padding: 1px 5px;
    border-radius: 3px;
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.3px;
    white-space: nowrap;
  }
  .model-tools-tag {
    background: var(--accent);
    color: var(--bg);
  }
  .model-free-tag {
    background: var(--success);
    color: var(--bg);
  }

  .model-empty {
    padding: 16px;
    text-align: center;
    color: var(--text-muted);
    font-size: 12px;
  }
</style>
