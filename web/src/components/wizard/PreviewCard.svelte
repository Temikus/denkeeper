<script>
  // The agent as configured so far. Empty until a provider exists; fills in
  // as each step is answered. compact is the one-line mobile version.
  let {
    name = '',
    emoji = '',
    model = '',
    provider = '',
    tier = '',
    supervised = false,
    chatApp = '',
    greeting = '',
    keyWorks = false,
    compact = false,
  } = $props()

  let empty = $derived(!provider && !name)
  let initial = $derived((name || '?').charAt(0).toUpperCase())
  let tierLabel = $derived(tier ? tier.charAt(0).toUpperCase() + tier.slice(1) : '')
  let subline = $derived(
    model ? (compact ? `${model}${tierLabel ? ' · ' + tierLabel : ''}` : `${model}${provider ? ' via ' + provider : ''}`)
      : provider ? `Runs on ${provider}` : '',
  )
</script>

<div class="preview" class:compact class:empty data-testid={compact ? 'wizard-preview-compact' : 'wizard-preview'}>
  {#if empty}
    <span class="avatar placeholder" aria-hidden="true"></span>
    <p class="empty-text">Your agent takes shape here as you go.</p>
  {:else}
    <div class="head">
      <span class="avatar" class:has-emoji={!!emoji} aria-hidden="true">{emoji || initial}</span>
      <div class="who">
        <span class="name" class:unnamed={!name}>{name || 'Unnamed agent'}</span>
        {#if subline}<span class="sub">{subline}</span>{/if}
      </div>
      {#if compact && chatApp}
        <span class="preview-chip ok">{chatApp}</span>
      {/if}
    </div>
    {#if greeting && !compact}
      <p class="greeting">{greeting}</p>
    {/if}
    {#if !compact}
      <div class="chips">
        {#if keyWorks && !model}<span class="preview-chip ok">Key works</span>{/if}
        {#if tierLabel}<span class="preview-chip accent">{tierLabel}</span>{/if}
        {#if supervised}<span class="preview-chip">+ supervisor</span>{/if}
        {#if model}
          {#if chatApp}<span class="preview-chip ok">{chatApp}</span>{:else}<span class="preview-chip dashed">No chat app yet</span>{/if}
        {/if}
      </div>
    {/if}
  {/if}
</div>

<style>
  .preview {
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding: 20px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 12px;
  }
  .preview.empty {
    background: none;
    border-style: dashed;
  }
  .preview.compact {
    padding: 12px;
    gap: 10px;
    border-radius: 10px;
  }
  .preview.compact.empty { flex-direction: row; align-items: center; }

  .head {
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .avatar {
    flex-shrink: 0;
    width: 44px;
    height: 44px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--accent);
    color: #fff;
    font-size: 18px;
    font-weight: 700;
  }
  .avatar.has-emoji {
    background: rgba(var(--accent-rgb), 0.12);
    font-size: 22px;
  }
  .avatar.placeholder {
    background: var(--surface);
    border: 1px dashed var(--border);
  }
  .compact .avatar { width: 36px; height: 36px; font-size: 15px; }
  .compact .avatar.has-emoji { font-size: 18px; }

  .who {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
    flex: 1;
  }
  .name { font-size: 16px; font-weight: 700; }
  .name.unnamed { color: var(--text-muted); }
  .compact .name { font-size: 15px; }
  .sub {
    font-size: 13px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .compact .sub { font-size: 12px; }
  .empty-text { font-size: 13px; color: var(--text-muted); }

  .greeting {
    align-self: flex-start;
    max-width: 100%;
    padding: 10px 14px;
    background: var(--surface);
    border-radius: 14px 14px 14px 4px;
    font-size: 14px;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .preview-chip {
    padding: 4px 10px;
    border-radius: 999px;
    background: var(--surface);
    font-size: 12px;
    font-weight: 500;
    color: var(--text-muted);
    white-space: nowrap;
  }
  .preview-chip.accent { background: rgba(var(--accent-rgb), 0.1); color: var(--accent); font-weight: 600; }
  .preview-chip.ok { background: rgba(61, 143, 98, 0.12); color: var(--success); font-weight: 600; }
  .preview-chip.dashed { background: none; border: 1px dashed var(--border); font-weight: 400; }
</style>
