<script>
  import { onMount } from 'svelte'
  import { api } from '../../api.js'
  import { TONES, EMOJI_SUGGESTIONS, DEFAULT_HOUSE_RULES, houseRules } from './wizardContent.js'

  // draft: { displayName, emoji, tone, customTheme, rules, saved }
  // rules is null until loaded from the agent's SOUL.md (or the defaults).
  let { draft = $bindable(), ready = $bindable(false), agentName = '' } = $props()

  let editingRules = $state(false)
  let moreEmoji = $state(false)
  let saving = $state(false)
  let error = $state('')

  let rules = $derived(houseRules(draft.rules ?? DEFAULT_HOUSE_RULES))
  let theme = $derived(draft.tone === 'custom' ? draft.customTheme.trim() : TONES.find(t => t.id === draft.tone)?.theme || '')

  $effect(() => {
    ready = !!draft.displayName.trim() && !!theme && !saving
  })

  onMount(async () => {
    if (draft.rules !== null || !agentName) return
    try {
      const soul = await api.getPersona(agentName, 'soul')
      draft.rules = soul?.content?.trim() || DEFAULT_HOUSE_RULES
    } catch {
      draft.rules = DEFAULT_HOUSE_RULES
    }
  })

  export async function submit() {
    error = ''
    saving = true
    try {
      await api.updateIdentity(agentName, { name: draft.displayName.trim(), emoji: draft.emoji.trim(), theme })
      await api.updatePersona(agentName, 'soul', draft.rules ?? DEFAULT_HOUSE_RULES)
      draft.saved = true
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
  <div class="identity">
    <div class="wz-row">
      <div class="wz-field emoji-field">
        <label class="wz-label" for="wizard-persona-emoji">Emoji</label>
        <input id="wizard-persona-emoji" class="wz-input emoji-input" maxlength="16" bind:value={draft.emoji} disabled={saving} data-testid="wizard-persona-emoji" />
      </div>
      <div class="wz-field">
        <label class="wz-label" for="wizard-persona-name">Display name</label>
        <input id="wizard-persona-name" class="wz-input" maxlength="64" placeholder="Den" bind:value={draft.displayName} disabled={saving} data-testid="wizard-persona-name" />
      </div>
    </div>
    <div class="emoji-row" role="group" aria-label="Suggested emoji">
      <span class="wz-hint">Try</span>
      {#each EMOJI_SUGGESTIONS as e (e)}
        <button type="button" class="emoji-btn" class:selected={draft.emoji === e} aria-pressed={draft.emoji === e} onclick={() => { draft.emoji = e }} disabled={saving}>{e}</button>
      {/each}
      {#if !moreEmoji}
        <button type="button" class="wz-link muted" onclick={() => { moreEmoji = true }}>Other…</button>
      {:else}
        <span class="wz-hint">Type or paste any emoji in the box above.</span>
      {/if}
    </div>
  </div>

  <fieldset class="wz-field">
    <legend class="wz-label">Tone</legend>
    <div class="wz-choices" style="--wz-cols: 3" data-testid="wizard-persona-tone">
      {#each TONES as t (t.id)}
        <label class="wz-choice">
          <input type="radio" name="tone" value={t.id} bind:group={draft.tone} disabled={saving} />
          <span class="wz-choice-title">{t.label}</span>
          <span class="wz-choice-caption">{t.caption}</span>
        </label>
      {/each}
    </div>
    <label class="custom-tone">
      <input type="radio" name="tone" value="custom" bind:group={draft.tone} disabled={saving} />
      Describe it in your own words
    </label>
    {#if draft.tone === 'custom'}
      <textarea class="wz-input" rows="2" maxlength="500" aria-label="Describe the tone" placeholder="e.g. dry-witted research partner who cites sources" bind:value={draft.customTheme} disabled={saving} data-testid="wizard-persona-theme"></textarea>
    {/if}
  </fieldset>

  <div class="wz-panel">
    <div class="rules-head">
      <span class="wz-panel-title">House rules <span class="pill">{rules.length}</span></span>
      <button type="button" class="wz-link" onclick={() => { draft.rules ??= DEFAULT_HOUSE_RULES; editingRules = !editingRules }} aria-expanded={editingRules}>{editingRules ? 'Done' : 'Edit rules'}</button>
    </div>
    {#if editingRules}
      <textarea class="wz-input" rows="10" bind:value={draft.rules} disabled={saving} aria-label="House rules" data-testid="wizard-persona-guidelines"></textarea>
      <span class="wz-hint">One rule per paragraph. Your agent reads these every turn.</span>
    {:else}
      <ul class="rules">
        {#each rules.slice(0, 2) as r, i (i)}<li>{r.split(/(?<=\.)\s/)[0]}</li>{/each}
        {#if rules.length > 2}<li class="more">and {rules.length - 2} more. The defaults are a good start.</li>{/if}
      </ul>
    {/if}
  </div>

  {#if error}<p class="inline-error" role="alert">{error}</p>{/if}
</div>

<style>
  .identity { display: flex; flex-direction: column; gap: 10px; }
  .emoji-field { flex: 0 0 72px !important; }
  .emoji-input { text-align: center; font-size: 20px; padding: 7px 4px; }
  .emoji-row { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
  .emoji-btn {
    width: 34px;
    height: 34px;
    border-radius: 8px;
    border: 1px solid transparent;
    background: var(--surface);
    font-size: 17px;
    cursor: pointer;
  }
  .emoji-btn.selected { border-color: rgba(var(--accent-rgb), 0.5); background: rgba(var(--accent-rgb), 0.12); }
  fieldset { border: none; min-width: 0; }
  legend { margin-bottom: 6px; }
  .custom-tone { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text-muted); cursor: pointer; padding-top: 4px; }
  .custom-tone input { accent-color: var(--accent); }
  .rules-head { display: flex; justify-content: space-between; align-items: center; }
  .rules { list-style: none; display: flex; flex-direction: column; gap: 6px; font-size: 13px; }
  .rules li { padding-left: 16px; position: relative; }
  .rules li::before { content: '—'; position: absolute; left: 0; color: var(--text-muted); }
  .rules .more { color: var(--text-muted); }
  @media (max-width: 768px) {
    .identity .wz-row { flex-direction: row; }
    .emoji-field { flex: 0 0 64px !important; width: auto !important; }
    .emoji-btn { width: 40px; height: 40px; }
  }
</style>
