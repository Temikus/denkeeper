<script>
  /**
   * Accessible filter chip bar.
   *
   * Renders an ARIA radiogroup and implements the keyboard contract that role
   * advertises: the whole group is a single tab stop (roving tabindex) and
   * Arrow / Home / End move the selection between chips. Styling lives in
   * shared.css (.filter-chips / .chip / .chip-dot) so every chip bar in the
   * dashboard looks and behaves the same.
   *
   * items: [{ value, label, dot?, testid? }]
   */
  let {
    items = [],
    value = '',
    label = '',
    size = '',
    testid = undefined,
    onselect = () => {},
  } = $props()

  let buttons = $state([])

  // The chip that owns the group's tab stop: the selected one, or the first
  // chip when nothing matches the current value.
  let activeIndex = $derived.by(() => {
    const i = items.findIndex((it) => it.value === value)
    return i >= 0 ? i : 0
  })

  function move(index) {
    const item = items[index]
    if (!item) return
    onselect(item.value)
    buttons[index]?.focus()
  }

  function handleKeydown(e) {
    if (items.length === 0) return
    const current = buttons.indexOf(e.target)
    const from = current >= 0 ? current : activeIndex
    let next
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        next = (from + 1) % items.length
        break
      case 'ArrowLeft':
      case 'ArrowUp':
        next = (from - 1 + items.length) % items.length
        break
      case 'Home':
        next = 0
        break
      case 'End':
        next = items.length - 1
        break
      default:
        return
    }
    e.preventDefault()
    move(next)
  }
</script>

<!-- The group is deliberately not focusable: the radios carry the roving
     tabindex, and the keydown handler sees their events by bubbling. -->
<!-- svelte-ignore a11y_interactive_supports_focus -->
<div
  class="filter-chips"
  class:filter-chips-sm={size === 'sm'}
  role="radiogroup"
  aria-label={label}
  data-testid={testid}
  onkeydown={handleKeydown}
>
  {#each items as item, i (item.value)}
    <button
      class="chip"
      class:active={value === item.value}
      role="radio"
      aria-checked={value === item.value}
      tabindex={i === activeIndex ? 0 : -1}
      data-testid={item.testid}
      bind:this={buttons[i]}
      onclick={() => onselect(item.value)}
    >{#if item.dot}<span class="chip-dot" style="background: {item.dot}"></span>{/if}{item.label}</button>
  {/each}
</div>
