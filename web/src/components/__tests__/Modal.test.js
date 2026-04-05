import { describe, test, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import ModalWrapper from '../../test/ModalWrapper.svelte'

describe('Modal', () => {
  test('renders title and children content', () => {
    const { getByText, getByTestId } = render(ModalWrapper, {
      props: { title: 'Edit Item', onClose: vi.fn() },
    })
    expect(getByText('Edit Item')).toBeInTheDocument()
    expect(getByTestId('modal-content')).toHaveTextContent('Test content')
  })

  test('calls onClose on close button click', async () => {
    const onClose = vi.fn()
    const { getByLabelText } = render(ModalWrapper, {
      props: { title: 'Test', onClose },
    })
    await fireEvent.click(getByLabelText('Close'))
    expect(onClose).toHaveBeenCalledOnce()
  })

  test('calls onClose when clicking overlay backdrop', async () => {
    const onClose = vi.fn()
    const { container } = render(ModalWrapper, {
      props: { title: 'Test', onClose },
    })
    const overlay = container.querySelector('.overlay')
    // Click the overlay itself (not a child)
    await fireEvent.click(overlay)
    expect(onClose).toHaveBeenCalledOnce()
  })

  test('does NOT call onClose when clicking inside modal body', async () => {
    const onClose = vi.fn()
    const { getByTestId } = render(ModalWrapper, {
      props: { title: 'Test', onClose },
    })
    await fireEvent.click(getByTestId('modal-content'))
    expect(onClose).not.toHaveBeenCalled()
  })
})
