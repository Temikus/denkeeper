import { describe, test, expect } from 'vitest'
import { render, fireEvent, waitFor } from '@testing-library/svelte'
import ModelSelector from '../ModelSelector.svelte'

describe('ModelSelector', () => {
  test('renders text input with placeholder', () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    expect(input).toBeInTheDocument()
    expect(input).toHaveAttribute('placeholder', 'e.g. anthropic/claude-sonnet-4-20250514')
  })

  test('dropdown opens on focus and loads models', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelector('.model-dropdown')).toBeInTheDocument()
    })

    // Wait for models to load from MSW handler
    await waitFor(() => {
      const options = container.querySelectorAll('.model-option')
      expect(options.length).toBeGreaterThan(0)
    })
  })

  test('tools filter is checked by default and filters non-tool models', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelectorAll('.model-option').length).toBeGreaterThan(0)
    })

    // Tools checkbox should be checked
    const checkbox = container.querySelector('.model-filter input[type="checkbox"]')
    expect(checkbox.checked).toBe(true)

    // Only tool-supporting models should show (claude-3-opus, gpt-4o from fixtures)
    const options = container.querySelectorAll('.model-option')
    expect(options).toHaveLength(2)
  })

  test('unchecking tools filter shows all models', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelectorAll('.model-option').length).toBeGreaterThan(0)
    })

    const checkbox = container.querySelector('.model-filter input[type="checkbox"]')
    await fireEvent.click(checkbox)

    await waitFor(() => {
      // All 4 fixture models should show
      expect(container.querySelectorAll('.model-option')).toHaveLength(4)
    })
  })

  test('search filters models by name', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelectorAll('.model-option').length).toBeGreaterThan(0)
    })

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
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelectorAll('.model-option').length).toBeGreaterThan(0)
    })

    const toolsTags = container.querySelectorAll('.model-tools-tag')
    expect(toolsTags.length).toBeGreaterThan(0)
    expect(toolsTags[0]).toHaveTextContent('TOOLS')
  })

  test('shows FREE tag for zero-cost models', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelectorAll('.model-option').length).toBeGreaterThan(0)
    })

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
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelectorAll('.model-option').length).toBeGreaterThan(0)
    })

    const popCounts = container.querySelectorAll('.pop-count')
    expect(popCounts.length).toBeGreaterThan(0)

    const bars = container.querySelectorAll('.pop-bars')
    expect(bars.length).toBeGreaterThan(0)
  })

  test('displays pricing information', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelectorAll('.model-option').length).toBeGreaterThan(0)
    })

    const costs = container.querySelectorAll('.model-option-cost')
    expect(costs.length).toBeGreaterThan(0)
    // Claude 3 Opus: $15.00/$75.00 per M
    expect(costs[0].textContent).toContain('per M')
  })

  test('selecting a model updates the value and closes dropdown', async () => {
    const { container } = render(ModelSelector)
    const input = container.querySelector('.selector-input')
    await fireEvent.focus(input)

    await waitFor(() => {
      expect(container.querySelectorAll('.model-option').length).toBeGreaterThan(0)
    })

    const firstOption = container.querySelector('.model-option')
    await fireEvent.mouseDown(firstOption)

    // Dropdown should close
    await waitFor(() => {
      expect(container.querySelector('.model-dropdown')).not.toBeInTheDocument()
    })

    // Input should have the selected model ID
    expect(input.value).toBeTruthy()
  })
})
