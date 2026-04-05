import { describe, test, expect } from 'vitest'
import { render } from '@testing-library/svelte'
import StatusBadge from '../StatusBadge.svelte'

describe('StatusBadge', () => {
  test('renders pending with warn class', () => {
    const { container } = render(StatusBadge, { props: { status: 'pending' } })
    const badge = container.querySelector('.badge')
    expect(badge).toHaveClass('warn')
    expect(badge).toHaveTextContent('pending')
  })

  test('renders approved with success class', () => {
    const { container } = render(StatusBadge, { props: { status: 'approved' } })
    expect(container.querySelector('.badge')).toHaveClass('success')
  })

  test('renders denied with danger class', () => {
    const { container } = render(StatusBadge, { props: { status: 'denied' } })
    expect(container.querySelector('.badge')).toHaveClass('danger')
  })

  test('renders expired with muted class', () => {
    const { container } = render(StatusBadge, { props: { status: 'expired' } })
    expect(container.querySelector('.badge')).toHaveClass('muted')
  })

  test('renders ok with success class', () => {
    const { container } = render(StatusBadge, { props: { status: 'ok' } })
    expect(container.querySelector('.badge')).toHaveClass('success')
  })

  test('renders unknown status with muted fallback', () => {
    const { container } = render(StatusBadge, { props: { status: 'unknown' } })
    expect(container.querySelector('.badge')).toHaveClass('muted')
  })

  test('renders empty status with muted fallback', () => {
    const { container } = render(StatusBadge, { props: { status: '' } })
    expect(container.querySelector('.badge')).toHaveClass('muted')
  })
})
