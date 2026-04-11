import { describe, test, expect, vi } from 'vitest'
import { render, fireEvent, waitFor } from '@testing-library/svelte'
import FallbackRulesModal from '../FallbackRulesModal.svelte'

describe('FallbackRulesModal', () => {
  test('renders empty state when no rules', () => {
    const { container } = render(FallbackRulesModal, {
      props: { rules: [], onSave: vi.fn(), onClose: vi.fn() },
    })
    expect(container.querySelector('.empty-title')).toBeInTheDocument()
    expect(container.querySelector('.empty-title').textContent).toContain('No fallback rules')
  })

  test('renders existing rules with correct fields', async () => {
    const rules = [
      { trigger: 'rate_limit', action: 'wait_and_retry', max_retries: 3, backoff: 'exponential' },
      { trigger: 'error', action: 'switch_provider', provider: 'ollama', model: 'llama3' },
    ]
    const { container } = render(FallbackRulesModal, {
      props: { rules, onSave: vi.fn(), onClose: vi.fn() },
    })

    const rows = container.querySelectorAll('.rule-row')
    expect(rows).toHaveLength(2)

    // First rule should show wait_and_retry fields.
    expect(rows[0].querySelector('.rule-num').textContent).toBe('01')
    expect(rows[1].querySelector('.rule-num').textContent).toBe('02')
  })

  test('add rule button adds a new rule row', async () => {
    const rules = []
    const { container } = render(FallbackRulesModal, {
      props: { rules, onSave: vi.fn(), onClose: vi.fn() },
    })

    expect(container.querySelectorAll('.rule-row')).toHaveLength(0)

    const addBtn = container.querySelector('.btn-add')
    await fireEvent.click(addBtn)

    await waitFor(() => {
      expect(container.querySelectorAll('.rule-row')).toHaveLength(1)
    })
  })

  test('remove button removes a rule', async () => {
    const rules = [
      { trigger: 'rate_limit', action: 'wait_and_retry', max_retries: 3, backoff: 'exponential' },
    ]
    const { container } = render(FallbackRulesModal, {
      props: { rules, onSave: vi.fn(), onClose: vi.fn() },
    })

    expect(container.querySelectorAll('.rule-row')).toHaveLength(1)

    const removeBtn = container.querySelector('.btn-remove')
    await fireEvent.click(removeBtn)

    await waitFor(() => {
      expect(container.querySelectorAll('.rule-row')).toHaveLength(0)
    })
  })

  test('cancel calls onClose', async () => {
    const onClose = vi.fn()
    const { container } = render(FallbackRulesModal, {
      props: { rules: [], onSave: vi.fn(), onClose },
    })

    const cancelBtn = container.querySelector('.btn-ghost')
    await fireEvent.click(cancelBtn)

    expect(onClose).toHaveBeenCalled()
  })

  test('save calls onSave with current rules', async () => {
    const rules = [
      { trigger: 'error', action: 'switch_model', model: 'gpt-4o' },
    ]
    const onSave = vi.fn().mockResolvedValue(undefined)
    const { container } = render(FallbackRulesModal, {
      props: { rules, onSave, onClose: vi.fn() },
    })

    const saveBtn = container.querySelector('.btn-primary')
    await fireEvent.click(saveBtn)

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith([
        { trigger: 'error', action: 'switch_model', model: 'gpt-4o' },
      ])
    })
  })

  test('shows rule count in legend', () => {
    const rules = [
      { trigger: 'rate_limit', action: 'wait_and_retry', max_retries: 2, backoff: 'constant' },
      { trigger: 'error', action: 'switch_model', model: 'gpt-4o' },
    ]
    const { container } = render(FallbackRulesModal, {
      props: { rules, onSave: vi.fn(), onClose: vi.fn() },
    })

    const count = container.querySelector('.legend-count')
    expect(count.textContent).toContain('2 rules')
  })

  test('header close button calls onClose', async () => {
    const onClose = vi.fn()
    const { container } = render(FallbackRulesModal, {
      props: { rules: [], onSave: vi.fn(), onClose },
    })

    const closeBtn = container.querySelector('.btn-close')
    await fireEvent.click(closeBtn)

    expect(onClose).toHaveBeenCalled()
  })
})
