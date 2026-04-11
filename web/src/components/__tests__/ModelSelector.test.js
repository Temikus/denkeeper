import { describe, test, expect } from 'vitest'
import { render, fireEvent, waitFor } from '@testing-library/svelte'
import ModelSelector from '../ModelSelector.svelte'

// Helper: open dropdown and select "All providers" to trigger model load.
async function openAndLoadAll(container) {
  const input = container.querySelector('.selector-input')
  await fireEvent.focus(input)

  // Wait for the provider dropdown to appear.
  await waitFor(() => {
    expect(container.querySelector('.provider-select')).toBeInTheDocument()
  })

  // Select "All providers" to fetch every model.
  const provSelect = container.querySelector('.provider-select')
  await fireEvent.change(provSelect, { target: { value: '' } })

  // Wait for models to load from MSW handler.
  await waitFor(() => {
    const options = container.querySelectorAll('.model-option')
    expect(options.length).toBeGreaterThan(0)
  })
}

describe('ModelSelector', () => {
  test('renders text input with placeholder', () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    expect(input).toBeInTheDocument()
    expect(input).toHaveAttribute('placeholder', 'e.g. anthropic/claude-sonnet-4-20250514')
  })

  test('dropdown opens on focus and shows provider select', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelector('.model-dropdown')).toBeInTheDocument()
    })

    // Should show provider filter dropdown
    const provSelect = container.querySelector('.provider-select')
    expect(provSelect).toBeInTheDocument()

    // Should show prompt to pick a provider
    await waitFor(() => {
      const empty = container.querySelector('.model-empty')
      expect(empty).toBeInTheDocument()
    })
  })

  test('selecting a provider loads models from server', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelector('.provider-select')).toBeInTheDocument()
    })

    // Select a specific provider.
    const provSelect = container.querySelector('.provider-select')
    await fireEvent.change(provSelect, { target: { value: 'openrouter' } })

    await waitFor(() => {
      const options = container.querySelectorAll('.model-option')
      expect(options.length).toBeGreaterThan(0)
    })
  })

  test('provider dropdown is hidden when provider prop is set', async () => {
    const { container } = render(ModelSelector, { props: { provider: 'openrouter' } })
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelector('.model-dropdown')).toBeInTheDocument()
    })

    // Provider dropdown should NOT be rendered.
    expect(container.querySelector('.provider-select')).not.toBeInTheDocument()

    // Models should load directly since provider prop is set.
    await waitFor(() => {
      const options = container.querySelectorAll('.model-option')
      expect(options.length).toBeGreaterThan(0)
    })
  })

  test('tools filter is checked by default and filters non-tool models', async () => {
    const { container } = render(ModelSelector)
    await openAndLoadAll(container)

    // Tools checkbox should be checked
    const checkbox = container.querySelector('.model-filter input[type="checkbox"]')
    expect(checkbox.checked).toBe(true)

    // Only tool-supporting models should show (claude-3-opus, gpt-4o from fixtures)
    const options = container.querySelectorAll('.model-option')
    expect(options).toHaveLength(2)
  })

  test('unchecking tools filter shows all models', async () => {
    const { container } = render(ModelSelector)
    await openAndLoadAll(container)

    const checkbox = container.querySelector('.model-filter input[type="checkbox"]')
    await fireEvent.click(checkbox)

    await waitFor(() => {
      // All 4 fixture models should show
      expect(container.querySelectorAll('.model-option')).toHaveLength(4)
    })
  })

  test('search filters models by name', async () => {
    const { container } = render(ModelSelector)
    await openAndLoadAll(container)

    const searchInput = container.querySelector('.model-search')
    await fireEvent.input(searchInput, { target: { value: 'opus' } })

    await waitFor(() => {
      const options = container.querySelectorAll('.model-option')
      expect(options).toHaveLength(1)
    })
  })

  test('sort buttons are rendered with popularity active by default', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelector('.model-sortbar')).toBeInTheDocument()
    })

    const buttons = container.querySelectorAll('.sort-btn')
    expect(buttons).toHaveLength(3)
    // Popularity should be active
    expect(buttons[2]).toHaveTextContent('Popularity')
    expect(buttons[2]).toHaveClass('active')
  })

  test('shows TOOLS tag for tool-supporting models', async () => {
    const { container } = render(ModelSelector)
    await openAndLoadAll(container)

    const toolsTags = container.querySelectorAll('.model-tools-tag')
    expect(toolsTags.length).toBeGreaterThan(0)
    expect(toolsTags[0]).toHaveTextContent('TOOLS')
  })

  test('shows FREE tag for zero-cost models', async () => {
    const { container } = render(ModelSelector)
    await openAndLoadAll(container)

    // Uncheck tools to see the free model
    const checkbox = container.querySelector('.model-filter input[type="checkbox"]')
    await fireEvent.click(checkbox)

    await waitFor(() => {
      const freeTags = container.querySelectorAll('.model-free-tag')
      expect(freeTags).toHaveLength(1)
      expect(freeTags[0]).toHaveTextContent('FREE')
    })
  })

  test('shows popularity bars and count', async () => {
    const { container } = render(ModelSelector)
    await openAndLoadAll(container)

    const popCounts = container.querySelectorAll('.pop-count')
    expect(popCounts.length).toBeGreaterThan(0)

    const bars = container.querySelectorAll('.pop-bars')
    expect(bars.length).toBeGreaterThan(0)
  })

  test('displays pricing information', async () => {
    const { container } = render(ModelSelector)
    await openAndLoadAll(container)

    const costs = container.querySelectorAll('.model-option-cost')
    expect(costs.length).toBeGreaterThan(0)
    // Claude 3 Opus: $15.00/$75.00 per M
    expect(costs[0].textContent).toContain('per M')
  })

  test('selecting a model updates the value and closes dropdown', async () => {
    const { container } = render(ModelSelector)
    await openAndLoadAll(container)

    const firstOption = container.querySelector('.model-option')
    await fireEvent.mouseDown(firstOption)

    // Dropdown should close
    await waitFor(() => {
      expect(container.querySelector('.model-dropdown')).not.toBeInTheDocument()
    })

    // Input should have the selected model ID
    const input = container.querySelector('.selector-input')
    expect(input.value).toBeTruthy()
  })
})
