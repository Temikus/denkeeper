<script>
  import { onMount } from 'svelte'
  import { api } from '../api.js'
  import ModelSelector from './ModelSelector.svelte'

  let { rules = $bindable([]), agentProvider = '', onSave, onClose } = $props()

  let saving = $state(false)
  let providers = $state([])

  const triggerOptions = [
    { value: 'rate_limit', label: 'Rate Limited' },
    { value: 'error', label: 'Error' },
    { value: 'cost_limit', label: 'Cost Limit Reached' },
  ]

  const actionOptions = [
    { value: 'switch_provider', label: 'Switch Provider' },
    { value: 'switch_model', label: 'Switch Model' },
    { value: 'wait_and_retry', label: 'Wait & Retry' },
  ]

  const scopeOptions = [
    { value: 'soft', label: 'Soft' },
    { value: 'hard', label: 'Hard' },
  ]

  const backoffOptions = ['exponential', 'constant']

  function triggerLabel(v) {
    return triggerOptions.find(o => o.value === v)?.label || v
  }

  // Evaluation timing label for each trigger type.
  function triggerTiming(v) {
    return v === 'cost_limit' ? 'pre-call' : 'post-call'
  }

  function defaultProviderForRule() {
    return agentProvider || providers[0] || ''
  }

  function addRule() {
    rules = [...rules, {
      trigger: 'rate_limit',
      action: 'switch_provider',
      provider: defaultProviderForRule(),
      model: '',
      scope: 'soft',
      max_retries: 3,
      backoff: 'exponential',
    }]
  }

  function removeRule(idx) {
    rules = rules.filter((_, i) => i !== idx)
  }

  function moveRule(from, to) {
    const next = [...rules]
    const [item] = next.splice(from, 1)
    next.splice(to, 0, item)
    rules = next
  }

  // When the user picks a trigger or action, ensure required fields are populated.
  function onTriggerChange(idx) {
    if (rules[idx].trigger === 'cost_limit' && !rules[idx].scope) {
      rules[idx].scope = 'soft'
    }
  }

  function onActionChange(idx) {
    if ((rules[idx].action === 'switch_provider' || rules[idx].action === 'switch_model') && !rules[idx].provider) {
      rules[idx].provider = defaultProviderForRule()
    }
  }

  // ModelSelector emits (modelId, providerName) on selection.
  function onModelPicked(idx, _modelId, providerName) {
    if (providerName) {
      rules[idx].provider = providerName
    }
  }

  async function save() {
    saving = true
    try {
      await onSave(rules)
    } finally {
      saving = false
    }
  }

  onMount(async () => {
    try {
      const data = await api.llmProviders()
      providers = (data.providers || []).filter(p => p.enabled).map(p => p.name)
    } catch {
      providers = []
    }
  })
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="overlay" onclick={onClose} onkeydown={(e) => e.key === 'Escape' && onClose()}>
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal" onclick={(e) => e.stopPropagation()}>
    <div class="modal-header">
      <div>
        <h2 class="modal-title">Fallback Rules</h2>
        <p class="modal-subtitle">Ordered contingency rules for LLM calls. First matching rule wins.</p>
      </div>
      <button class="btn-close" onclick={onClose}>&#x2715;</button>
    </div>

    <div class="modal-legend">
      <span class="legend-label">Evaluation</span>
      <span class="legend-tag legend-post">post-call</span>
      <span class="legend-tag legend-pre">pre-call</span>
      <div class="legend-spacer"></div>
      <span class="legend-count">{rules.length} rule{rules.length !== 1 ? 's' : ''}</span>
    </div>

    <div class="modal-body">
      {#if rules.length === 0}
        <div class="empty-state">
          <div class="empty-title">No fallback rules configured</div>
          <div class="empty-desc">Add rules to handle errors and rate limits gracefully</div>
        </div>
      {:else}
        <div class="rules-list">
          {#each rules as rule, idx}
            <div class="rule-row">
              <div class="rule-handle">
                <span class="rule-num">{String(idx + 1).padStart(2, '0')}</span>
                <div class="rule-arrows">
                  <button class="arrow-btn" disabled={idx === 0} onclick={() => moveRule(idx, idx - 1)} title="Move up">&#x25B2;</button>
                  <button class="arrow-btn" disabled={idx === rules.length - 1} onclick={() => moveRule(idx, idx + 1)} title="Move down">&#x25BC;</button>
                </div>
              </div>

              <div class="rule-body">
                <div class="rule-condition">
                  <span class="rule-if">IF</span>
                  <select class="input input-accent" bind:value={rule.trigger} onchange={() => onTriggerChange(idx)}>
                    {#each triggerOptions as opt}
                      <option value={opt.value}>{opt.label}</option>
                    {/each}
                  </select>
                  {#if rule.trigger === 'cost_limit'}
                    <span class="rule-label">scope</span>
                    <select class="input" bind:value={rule.scope}>
                      {#each scopeOptions as opt}
                        <option value={opt.value}>{opt.label}</option>
                      {/each}
                    </select>
                  {/if}
                  <span class="rule-arrow">&rarr;</span>
                  <select class="input" bind:value={rule.action} onchange={() => onActionChange(idx)}>
                    {#each actionOptions as opt}
                      <option value={opt.value}>{opt.label}</option>
                    {/each}
                  </select>
                </div>

                <div class="rule-fields">
                  {#if rule.action === 'switch_provider'}
                    <div class="field-group">
                      <span class="field-label">Provider</span>
                      <select class="input" bind:value={rule.provider}>
                        {#each providers as p}
                          <option value={p}>{p}</option>
                        {/each}
                      </select>
                    </div>
                    <div class="field-group field-grow">
                      <span class="field-label">Model <span class="field-optional">(optional)</span></span>
                      <ModelSelector
                        bind:value={rule.model}
                        provider={rule.provider}
                        onchange={(m, p) => onModelPicked(idx, m, p)}
                      />
                    </div>
                  {:else if rule.action === 'switch_model'}
                    <div class="field-group">
                      <span class="field-label">Provider</span>
                      <select class="input" bind:value={rule.provider}>
                        {#each providers as p}
                          <option value={p}>{p}</option>
                        {/each}
                      </select>
                    </div>
                    <div class="field-group field-grow">
                      <span class="field-label">Fallback Model</span>
                      <ModelSelector
                        bind:value={rule.model}
                        provider={rule.provider}
                        onchange={(m, p) => onModelPicked(idx, m, p)}
                      />
                    </div>
                  {:else if rule.action === 'wait_and_retry'}
                    <div class="field-group">
                      <span class="field-label">Max Retries</span>
                      <input type="number" class="input input-num" bind:value={rule.max_retries} min="1" max="10" />
                    </div>
                    <div class="field-group">
                      <span class="field-label">Backoff</span>
                      <select class="input" bind:value={rule.backoff}>
                        {#each backoffOptions as b}
                          <option value={b}>{b}</option>
                        {/each}
                      </select>
                    </div>
                  {/if}
                </div>
              </div>

              <button class="btn-remove" onclick={() => removeRule(idx)} title="Remove rule">&#x00D7;</button>
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <div class="modal-footer">
      <button class="btn-add" onclick={addRule}>
        <span class="btn-add-icon">+</span> Add Rule
      </button>
      <div class="footer-actions">
        <button class="btn btn-ghost" onclick={onClose}>Cancel</button>
        <button class="btn btn-primary" onclick={save} disabled={saving}>
          {saving ? 'Saving...' : 'Save Rules'}
        </button>
      </div>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
    background: var(--overlay-bg);
  }

  .modal {
    position: relative;
    width: 100%;
    max-width: 680px;
    max-height: 88vh;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 12px;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .modal-header {
    padding: 22px 28px 16px;
    border-bottom: 1px solid var(--border);
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    flex-shrink: 0;
  }

  .modal-title {
    margin: 0;
    font-size: 18px;
    font-weight: 600;
  }

  .modal-subtitle {
    margin: 6px 0 0;
    font-size: 13px;
    color: var(--text-muted);
    line-height: 1.5;
  }

  .btn-close {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    padding: 6px 8px;
    font-size: 14px;
    line-height: 1;
    transition: color 0.15s, background 0.15s;
  }
  .btn-close:hover { color: var(--text); background: var(--hover-overlay); }

  .modal-legend {
    padding: 10px 28px;
    border-bottom: 1px solid var(--border);
    display: flex;
    gap: 8px;
    align-items: center;
    flex-shrink: 0;
    background: var(--surface);
  }

  .legend-label {
    font-size: 10px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.1em;
    font-family: monospace;
    font-weight: 600;
  }

  .legend-tag {
    font-size: 10px;
    padding: 2px 7px;
    border-radius: 4px;
    font-family: monospace;
  }
  .legend-post { color: var(--warn); background: color-mix(in srgb, var(--warn) 10%, transparent); border: 1px solid color-mix(in srgb, var(--warn) 30%, transparent); }
  .legend-pre { color: var(--accent); background: color-mix(in srgb, var(--accent) 8%, transparent); border: 1px solid color-mix(in srgb, var(--accent) 25%, transparent); }

  .legend-spacer { flex: 1; }

  .legend-count {
    font-size: 10px;
    color: var(--text-muted);
    font-family: monospace;
  }

  .modal-body {
    flex: 1;
    overflow-y: auto;
    padding: 16px 28px;
  }

  .empty-state {
    text-align: center;
    padding: 48px 20px;
    color: var(--text-muted);
  }
  .empty-title { font-size: 14px; font-weight: 500; color: var(--text-muted); }
  .empty-desc { font-size: 12px; margin-top: 6px; }

  .rules-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .rule-row {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    padding: 14px 16px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    transition: background 0.15s;
  }
  .rule-row:hover { background: var(--hover-overlay); }

  .rule-handle {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    padding-top: 4px;
    min-width: 24px;
  }

  .rule-num {
    font-size: 10px;
    color: var(--text-muted);
    font-family: monospace;
    font-weight: 700;
    opacity: 0.5;
  }

  .rule-arrows {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }

  .arrow-btn {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 8px;
    padding: 0;
    line-height: 1;
  }
  .arrow-btn:disabled { color: var(--border); cursor: default; }
  .arrow-btn:not(:disabled):hover { color: var(--accent); }

  .rule-body {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .rule-condition {
    display: flex;
    gap: 8px;
    align-items: center;
    flex-wrap: wrap;
  }

  .rule-if {
    font-size: 11px;
    color: var(--text-muted);
    font-weight: 500;
    margin-right: 2px;
  }

  .rule-arrow {
    font-size: 13px;
    color: var(--text-muted);
    margin: 0 2px;
  }

  .rule-label {
    font-size: 11px;
    color: var(--text-muted);
  }

  .rule-fields {
    display: flex;
    gap: 8px;
    align-items: flex-end;
    flex-wrap: wrap;
  }

  .field-grow {
    flex: 1 1 220px;
    min-width: 220px;
  }

  .field-group {
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .field-label {
    font-size: 10px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    font-family: monospace;
  }

  .field-optional { opacity: 0.6; }

  .input {
    padding: 7px 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    font-size: 13px;
    outline: none;
    transition: border-color 0.2s;
  }
  .input:focus { border-color: var(--accent); }

  .input-accent {
    background: color-mix(in srgb, var(--accent) 6%, transparent);
    border-color: color-mix(in srgb, var(--accent) 30%, transparent);
  }

  .input-num {
    width: 60px;
    text-align: center;
  }

  select.input {
    cursor: pointer;
    appearance: auto;
  }

  .btn-remove {
    background: none;
    border: 1px solid transparent;
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    padding: 4px 6px;
    font-size: 16px;
    line-height: 1;
    transition: color 0.15s, border-color 0.15s;
    margin-top: 2px;
    opacity: 0.5;
  }
  .btn-remove:hover { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 20%, transparent); opacity: 1; }

  .modal-footer {
    padding: 14px 28px 20px;
    border-top: 1px solid var(--border);
    display: flex;
    justify-content: space-between;
    align-items: center;
    flex-shrink: 0;
    background: var(--surface);
  }

  .btn-add {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 16px;
    background: transparent;
    border: 1px dashed var(--border);
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 13px;
    font-weight: 500;
    transition: color 0.15s, border-color 0.15s;
  }
  .btn-add:hover { border-color: var(--accent); color: var(--accent); }
  .btn-add-icon { font-size: 16px; line-height: 1; margin-top: -1px; }

  .footer-actions {
    display: flex;
    gap: 10px;
  }

  .btn {
    padding: 9px 20px;
    border-radius: 6px;
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.15s;
  }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-ghost {
    background: transparent;
    border: 1px solid var(--border);
    color: var(--text-muted);
  }
  .btn-ghost:hover { border-color: var(--text-muted); color: var(--text); }

  .btn-primary {
    background: var(--accent);
    border: 1px solid var(--accent);
    color: #fff;
    font-weight: 600;
  }
  .btn-primary:hover { background: var(--accent-hover); border-color: var(--accent-hover); }
</style>
