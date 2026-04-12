import { describe, test, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
import { createRawSnippet } from 'svelte'
import Collapsible from '../Collapsible.svelte'

// Helper: Svelte 5 requires a snippet for children.
// We use createRawSnippet to create one for testing.
function renderCollapsible(props = {}) {
  const children = createRawSnippet(() => ({
    render: () => '<p>Section content</p>',
  }))
  return render(Collapsible, { props: { title: 'Test Section', children, ...props } })
}

describe('Collapsible', () => {
  test('renders title and children when open', () => {
    renderCollapsible({ open: true })
    expect(screen.getByText('Test Section')).toBeInTheDocument()
    expect(screen.getByText('Section content')).toBeInTheDocument()
  })

  test('hides children when closed', () => {
    renderCollapsible({ open: false })
    expect(screen.getByText('Test Section')).toBeInTheDocument()
    expect(screen.queryByText('Section content')).not.toBeInTheDocument()
  })

  test('click toggles visibility', async () => {
    renderCollapsible({ open: true })
    expect(screen.getByText('Section content')).toBeInTheDocument()

    await fireEvent.click(screen.getByRole('button'))
    expect(screen.queryByText('Section content')).not.toBeInTheDocument()

    await fireEvent.click(screen.getByRole('button'))
    expect(screen.getByText('Section content')).toBeInTheDocument()
  })

  test('aria-expanded reflects open state', async () => {
    renderCollapsible({ open: false })
    const button = screen.getByRole('button')
    expect(button).toHaveAttribute('aria-expanded', 'false')

    await fireEvent.click(button)
    expect(button).toHaveAttribute('aria-expanded', 'true')
  })

  test('aria-controls links to section body', () => {
    renderCollapsible({ open: true, id: 'demo' })
    const button = screen.getByRole('button')
    expect(button).toHaveAttribute('aria-controls', 'section-demo')

    const region = screen.getByRole('region')
    expect(region).toHaveAttribute('id', 'section-demo')
    expect(region).toHaveAttribute('aria-labelledby', 'heading-demo')
  })
})
