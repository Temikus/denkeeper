<script>
  export let status = ''
  $: cls = {
    pending:  'warn',
    approved: 'success',
    denied:   'danger',
    expired:  'muted',
    ok:       'success',
  }[status] || 'muted'
</script>

<span class="badge {cls}">{status}</span>

<style>
  .badge {
    display: inline-block;
    padding: 2px 8px;
    border-radius: 10px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .warn    { background: rgba(240,169,88,0.2);  color: var(--warn); }
  .success { background: rgba(76,175,125,0.2);  color: var(--success); }
  .danger  { background: rgba(224,92,110,0.2);  color: var(--danger); }
  .muted   { background: var(--border); color: var(--text-muted); }
</style>
