import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

const EvalCoverage = (await import('../EvalCoverage.svelte')).default

const task = (category) => ({ category })

describe('EvalCoverage', () => {
  test('counts each kind, including the empty ones', () => {
    render(EvalCoverage, { tasks: [task('chat'), task('chat'), task('tool_heavy')] })

    expect(screen.getByText(/Coverage · 3 cases/)).toBeInTheDocument()
    expect(screen.getByTestId('coverage-chat')).toHaveTextContent('Chat / persona 2')
    expect(screen.getByTestId('coverage-tool_heavy')).toHaveTextContent('Tool-heavy 1')
    expect(screen.getByTestId('coverage-scheduled')).toHaveTextContent('Scheduled 0')
  })

  test('a missing history kind offers a narrowed suggestion pass', async () => {
    const onfill = vi.fn()
    render(EvalCoverage, { tasks: [task('chat')], onfill })

    expect(screen.queryByTestId('gap-chat')).not.toBeInTheDocument()
    await fireEvent.click(screen.getByTestId('gap-tool_heavy'))
    expect(onfill).toHaveBeenCalledWith('tool_heavy')
  })

  test('a missing probe kind offers Generate probes', async () => {
    const onfill = vi.fn()
    render(EvalCoverage, { tasks: [task('chat')], onfill })

    expect(screen.getByTestId('gap-probe')).toHaveTextContent('Generate probes')
    await fireEvent.click(screen.getByTestId('gap-probe'))
    expect(onfill).toHaveBeenCalledWith('probe')
  })

  test('the probe gap is disabled when there is no agent to probe', () => {
    render(EvalCoverage, { tasks: [task('chat')], onfill: vi.fn(), canProbe: false })
    expect(screen.getByTestId('gap-probe')).toBeDisabled()
    expect(screen.getByTestId('gap-tool_heavy')).not.toBeDisabled()
  })

  test('a kind the agent cannot produce is n/a, not a gap', () => {
    const notApplicable = { scheduled: 'no schedules', skill_command: 'no command skills' }
    render(EvalCoverage, { tasks: [task('chat')], onfill: vi.fn(), notApplicable, agent: 'pamela' })

    expect(screen.getByTestId('coverage-scheduled')).toHaveTextContent('Scheduled n/a')
    expect(screen.queryByTestId('gap-scheduled')).not.toBeInTheDocument()
    expect(screen.queryByTestId('gap-skill_command')).not.toBeInTheDocument()
    expect(screen.getByTestId('gap-tool_heavy')).toBeInTheDocument()
    expect(screen.getByTestId('coverage-skipped'))
      .toHaveTextContent('Not gaps for pamela: skill command (no command skills), scheduled (no schedules).')
  })

  test('cases of an n/a kind still count as present', () => {
    render(EvalCoverage, { tasks: [task('scheduled')], notApplicable: { scheduled: 'no schedules' } })
    expect(screen.getByTestId('coverage-scheduled')).toHaveTextContent('Scheduled 1')
  })

  test('the compact line leaves n/a kinds out of the missing count', () => {
    render(EvalCoverage, {
      tasks: [task('chat')], compact: true, notApplicable: { scheduled: 'no schedules' },
    })
    // tool_heavy, skill_command and probe are missing; scheduled is n/a.
    expect(screen.getByTestId('coverage-compact')).toHaveTextContent('3 kinds missing')
  })

  test('a set with every kind has no gap prompts', () => {
    const all = ['chat', 'skill_command', 'scheduled', 'tool_heavy', 'probe'].map(task)
    render(EvalCoverage, { tasks: all, onfill: vi.fn() })

    expect(screen.queryByTestId('coverage-gaps')).not.toBeInTheDocument()
  })

  test('the Quick check line matches the stratified draw', () => {
    const many = Array.from({ length: 12 }, (_, i) => task(i % 2 ? 'chat' : 'tool_heavy'))
    const { unmount } = render(EvalCoverage, { tasks: many })
    expect(screen.getByTestId('coverage-quick'))
      .toHaveTextContent('A Quick check draws 10 of the 12, spread evenly across the 2 kinds this set has.')
    unmount()

    render(EvalCoverage, { tasks: [task('chat'), task('chat')] })
    expect(screen.getByTestId('coverage-quick')).toHaveTextContent('A Quick check runs all 2 cases.')
  })

  test('compact form is one line with a link to the full view', async () => {
    const onsee = vi.fn()
    render(EvalCoverage, { tasks: [task('chat'), task('scheduled')], compact: true, onsee })

    const line = screen.getByTestId('coverage-compact')
    expect(line).toHaveTextContent('Chat / persona 1 · Scheduled 1')
    expect(line).toHaveTextContent('3 kinds missing')
    expect(screen.queryByTestId('coverage')).not.toBeInTheDocument()
    await fireEvent.click(screen.getByTestId('coverage-see'))
    expect(onsee).toHaveBeenCalled()
  })
})
