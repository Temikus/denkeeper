<script>
  import { setUpCheckIn, CHECK_IN_LABEL } from '../checkIn.js'

  // Offers a check-in for a den agent that has none. checkIns is null when the
  // schedules couldn't be read; then nothing is offered.
  let { agent = '', checkIns = null, ondone = () => {} } = $props()

  let saving = $state(false)
  let error = $state('')

  const needed = $derived(!!agent && !!checkIns && !checkIns.has(agent))

  async function setUp() {
    saving = true
    error = ''
    try {
      await setUpCheckIn(agent)
      await ondone()
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }
</script>

{#if needed}
  <span class="check-in">
    <button type="button" class="btn-ghost btn-sm" onclick={setUp} disabled={saving}
      title="Adds a check-tasks skill and a schedule that runs it with no chat">
      {saving ? 'Setting up…' : `Check ${CHECK_IN_LABEL}`}
    </button>
    {#if error}<span class="inline-error" role="alert">{error}</span>{/if}
  </span>
{/if}

<style>
  .check-in { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px; }
</style>
