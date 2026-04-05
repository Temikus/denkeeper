import { describe, test, expect } from 'vitest'
import { render } from '@testing-library/svelte'
import ErrorBanner from '../ErrorBanner.svelte'

describe('ErrorBanner', () => {
  test('renders message when truthy', () => {
    const { getByText } = render(ErrorBanner, { props: { message: 'Something broke' } })
    expect(getByText('Something broke')).toBeInTheDocument()
  })

  test('renders nothing when message is empty string', () => {
    const { container } = render(ErrorBanner, { props: { message: '' } })
    expect(container.querySelector('.banner')).toBeNull()
  })

  test('renders nothing when message is undefined (default)', () => {
    const { container } = render(ErrorBanner)
    expect(container.querySelector('.banner')).toBeNull()
  })
})
