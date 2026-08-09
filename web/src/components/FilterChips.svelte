<script>
  /**
   * Accessible filter chip bar, single- or multi-select.
   *
   * Single select (default) renders an ARIA radiogroup and implements the
   * keyboard contract that role advertises: the whole group is a single tab
   * stop (roving tabindex) and Arrow / Home / End move the selection.
   *
   * With `multiple`, `value` is an array and the bar becomes a toolbar of
   * aria-pressed toggle buttons — the role ARIA pairs with roving tabindex and
   * with toggles, so the single-tab-stop contract stays conformant. Arrows move
   * focus *without* selecting; Enter/Space toggle natively (these are real
   * <button>s, so the browser fires click — handleKeydown deliberately doesn't
   * intercept them). `onselect` still fires once per interaction, but carries
   * the next array.
   *
   * The `''` item is the clear-all chip ("All"): choosing it emits `[]`, and it
   * renders active whenever nothing is selected — so a caller's item list is
   * identical in both modes.
   *
   * Styling lives in shared.css (.filter-chips / .chip / .chip-dot /
   * .chip-mark) so every chip bar in the dashboard looks and behaves the same.
   *
   * items: [{ value, label, dot?, testid? }]
   */
  let {
    items = [],
    value = '',
    label = '',
    size = '',
    multiple = false,
    testid = undefined,
    onselect = () => {},
  } = $props()

  let buttons = $state([])

  // Multi-select tolerates a stale scalar `value` (e.g. a caller mid-migration)
  // rather than throwing on `.includes`.
  let selected = $derived(
    multiple ? (Array.isArray(value) ? value : (value ? [value] : [])) : [],
  )

  function isActive(itemValue) {
    if (!multiple) return value === itemValue
    if (itemValue === '') return selected.length === 0
    return selected.includes(itemValue)
  }

  // The chip that owns the group's tab stop: the first selected one, or the
  // first chip when nothing matches. In multi-select, arrows move focus without
  // selecting, so the tab stop has to follow the last-focused chip instead —
  // otherwise tabbing away and back would snap focus back to the selection.
  let focusedIndex = $state(null)
  let activeIndex = $derived.by(() => {
    if (multiple && focusedIndex !== null && focusedIndex < items.length) return focusedIndex
    const i = items.findIndex((it) => isActive(it.value))
    return i >= 0 ? i : 0
  })

  function choose(itemValue) {
    if (!multiple) {
      onselect(itemValue)
      return
    }
    // The clear-all chip resets rather than joining the set.
    if (itemValue === '') {
      onselect([])
      return
    }
    onselect(
      selected.includes(itemValue)
        ? selected.filter((v) => v !== itemValue)
        : [...selected, itemValue],
    )
  }

  function move(index) {
    const item = items[index]
    if (!item) return
    // Toggles are pressed deliberately, so arrows only move focus; radios
    // select on arrow, which is the pattern that role prescribes.
    if (multiple) focusedIndex = index
    else onselect(item.value)
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

<!-- The container is deliberately not focusable: the chips carry the roving
     tabindex, and the keydown handler sees their events by bubbling. (The
     a11y suppression is for the radiogroup case; toolbar is fine unfocused.) -->
<!-- svelte-ignore a11y_interactive_supports_focus -->
<div
  class="filter-chips"
  class:filter-chips-sm={size === 'sm'}
  role={multiple ? 'toolbar' : 'radiogroup'}
  aria-label={label}
  data-testid={testid}
  onkeydown={handleKeydown}
>
  {#each items as item, i (item.value)}
    <button
      class="chip"
      class:active={isActive(item.value)}
      role={multiple ? undefined : 'radio'}
      aria-checked={multiple ? undefined : isActive(item.value)}
      aria-pressed={multiple ? isActive(item.value) : undefined}
      tabindex={i === activeIndex ? 0 : -1}
      data-testid={item.testid}
      bind:this={buttons[i]}
      onclick={() => choose(item.value)}
    >{#if multiple}<span class="chip-mark"></span>{:else if item.dot}<span class="chip-dot" style="background: {item.dot}"></span>{/if}{item.label}</button>
  {/each}
</div>
