<script>
  // Step 0: what setup needs, plus the config-file way out.
  let { onUseConfigFile, busy = false } = $props()
</script>

<div class="welcome">
  <h3 class="needs-title">What you'll need</h3>
  <ul class="needs">
    <li>
      <span class="need-title">An API key</span>
      <span class="need-text">From Anthropic, OpenAI or OpenRouter. Using Ollama on this machine? No key needed.</span>
    </li>
    <li>
      <span class="need-title">A bot token, if you want chat apps</span>
      <span class="need-text">Optional. You can skip this and use the web chat.</span>
    </li>
    <li>
      <span class="need-title">About three minutes</span>
      <span class="need-text">Progress is saved as you go, so you can close the tab and come back.</span>
    </li>
  </ul>
  <p class="config-file">
    Prefer config files?
    <button type="button" class="wz-link" onclick={onUseConfigFile} disabled={busy} data-testid="wizard-use-config">Skip and edit denkeeper.toml</button>
  </p>
</div>

<style>
  .welcome { display: flex; flex-direction: column; gap: 24px; }
  .needs-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--text-muted);
  }
  .needs { list-style: none; border-top: 1px solid var(--border); margin-top: -12px; }
  .needs li {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 16px 0;
    border-bottom: 1px solid var(--border);
  }
  .need-title { font-size: 15px; font-weight: 600; }
  .need-text { font-size: 14px; color: var(--text-muted); }
  .config-file { font-size: 13px; color: var(--text-muted); display: flex; gap: 6px; flex-wrap: wrap; }
</style>
