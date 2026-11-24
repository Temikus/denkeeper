import { describe, test, expect } from 'vitest'
import { render, screen } from '@testing-library/svelte'
import ApprovalRisk from '../ApprovalRisk.svelte'

describe('ApprovalRisk', () => {
  test('shows the warning and every flagged name and value, as text', () => {
    const { container } = render(ApprovalRisk, {
      props: {
        risk: {
          level: 'high',
          warning: 'Adds a tool server: starts a program on this host or connects to a remote server.',
          flagged: [
            { name: 'command', value: '"npx"' },
            { name: 'env', value: '{"X":"<script>alert(1)</script>"}' },
          ],
        },
      },
    })
    expect(screen.getByText('High risk')).toBeInTheDocument()
    expect(screen.getByText(/Adds a tool server: starts a program/)).toBeInTheDocument()
    expect(screen.getByText('command')).toBeInTheDocument()
    expect(screen.getByText('"npx"')).toBeInTheDocument()
    expect(screen.getByText('env')).toBeInTheDocument()
    expect(screen.getByText('{"X":"<script>alert(1)</script>"}')).toBeInTheDocument()
    expect(container.querySelector('script')).toBeNull()
  })
})
