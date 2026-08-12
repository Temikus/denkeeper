import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'

const SkillInvocation = (await import('../SkillInvocation.svelte')).default

// Scheduled and ambient skills are identical in frontmatter — scheduled_by is
// the difference, and it comes from [[schedules]].
const scheduled = { name: 'hello', agent: 'default', triggers: [], scheduled_by: ['nightly'] }
const ambient = { name: 'hello', agent: 'default', triggers: [] }
const commanded = { name: 'inbox-triage', agent: 'pamela', triggers: ['command:triage'] }

describe('SkillInvocation', () => {
  // A scheduled run has no message, so asking for one was the original bug.
  test('a skill a schedule fires defaults to a scheduled run, with no message box', () => {
    render(SkillInvocation, { props: { skill: scheduled, onrun: vi.fn() } })

    expect(screen.getByText('On a schedule')).toHaveClass('active')
    expect(screen.queryByPlaceholderText(/summarise what happened/)).not.toBeInTheDocument()
    expect(screen.getByText(/No message to write/)).toBeInTheDocument()
    // Naming the schedule is what makes the preview checkable against reality.
    expect(screen.getByText('nightly')).toBeInTheDocument()
  })

  // The bug this replaced: no triggers was read as "scheduled", so an ambient
  // skill was previewed with a header it never receives.
  test('a skill with no triggers and no schedule defaults to a chat message', () => {
    render(SkillInvocation, { props: { skill: ambient, onrun: vi.fn() } })

    expect(screen.getByText('As a chat message')).toHaveClass('active')
    expect(screen.getByPlaceholderText(/summarise what happened/)).toBeInTheDocument()
    expect(screen.queryByText(/No message to write/)).not.toBeInTheDocument()
    expect(screen.getByText(/matches ordinary turns/)).toBeInTheDocument()
  })

  test('a skill with a command trigger defaults to command mode', () => {
    const { container } = render(SkillInvocation, { props: { skill: commanded, onrun: vi.fn() } })

    expect(screen.getByText('As a command')).toHaveClass('active')
    // The trigger is a fixed prefix on the input, not something to retype.
    expect(container.querySelector('.command-prefix')).toHaveTextContent('/triage')
  })

  // Disabled rather than hidden: "this skill has no command" is information.
  test('command mode is disabled, not hidden, when there is no trigger', () => {
    render(SkillInvocation, { props: { skill: scheduled, onrun: vi.fn() } })

    const chip = screen.getByText('As a command')
    expect(chip).toBeDisabled()
    expect(screen.getByText(/no/)).toBeInTheDocument()
  })

  test('shows the literal scheduled header the agent will receive', () => {
    const { container } = render(SkillInvocation, {
      props: { skill: scheduled, timezone: 'Australia/Sydney', onrun: vi.fn() },
    })

    const outgoing = container.querySelector('.outgoing-body').textContent
    expect(outgoing).toMatch(/^\[Scheduled: hello \| /)
    expect(outgoing).toContain('Australia/Sydney')
    expect(outgoing).toMatch(/\d{4}-W\d{2}\]$/)
  })

  // Empty defaults made the common case — "run this as if it fired now" — an
  // act of typing, and left the header preview claiming a time nothing sent.
  test('fire time defaults to now in the agent timezone', () => {
    render(SkillInvocation, {
      props: { skill: scheduled, timezone: 'Australia/Sydney', onrun: vi.fn() },
    })

    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: 'Australia/Sydney', hourCycle: 'h23',
      year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
    }).formatToParts(new Date()).reduce((a, p) => ({ ...a, [p.type]: p.value }), {})

    expect(screen.getByLabelText('Fire date')).toHaveValue(
      `${parts.year}-${parts.month}-${parts.day}`)
    expect(screen.getByLabelText('Fire time of day')).toHaveValue(
      `${parts.hour}:${parts.minute}`)
  })

  // datetime-local opens a date-only popover in several browsers, so the time
  // was typable but not pickable. Two controls, both with a native picker.
  test('the time of day is its own control, and moves the previewed header', async () => {
    const { container } = render(SkillInvocation, {
      props: { skill: scheduled, timezone: 'Australia/Sydney', onrun: vi.fn() },
    })

    await fireEvent.input(screen.getByLabelText('Fire date'), { target: { value: '2026-08-11' } })
    await fireEvent.input(screen.getByLabelText('Fire time of day'), { target: { value: '01:00' } })

    // Stamped in the agent's zone (+10:00 in August), not the browser's — the
    // zone the label names is the one whose midnight rolls the dated key.
    expect(container.querySelector('.outgoing-body')).toHaveTextContent(
      '[Scheduled: hello | 2026-08-11T01:00:00+10:00 Australia/Sydney | 2026-W33]')

    await fireEvent.input(screen.getByLabelText('Fire time of day'), { target: { value: '23:30' } })
    expect(container.querySelector('.outgoing-body')).toHaveTextContent('2026-08-11T23:30:00+10:00')
  })

  test('the chosen fire time is sent as the instant it names in that zone', async () => {
    const onrun = vi.fn()
    render(SkillInvocation, {
      props: { skill: scheduled, timezone: 'Australia/Sydney', onrun },
    })

    await fireEvent.input(screen.getByLabelText('Fire date'), { target: { value: '2026-08-11' } })
    await fireEvent.input(screen.getByLabelText('Fire time of day'), { target: { value: '01:00' } })
    await fireEvent.click(screen.getByText('Run'))

    expect(onrun).toHaveBeenCalledWith(expect.objectContaining({
      as_of: '2026-08-10T15:00:00.000Z',
    }))
  })

  // Browsers let either half be cleared; a half-filled pair is not a time.
  test('clearing the date unpins the clock instead of sending a partial one', async () => {
    const onrun = vi.fn()
    render(SkillInvocation, { props: { skill: scheduled, timezone: 'UTC', onrun } })

    await fireEvent.input(screen.getByLabelText('Fire date'), { target: { value: '' } })
    await fireEvent.click(screen.getByText('Run'))

    expect(onrun.mock.calls[0][0].as_of).toBeUndefined()
  })

  test('Now resets a fiddled-with fire time', async () => {
    render(SkillInvocation, { props: { skill: scheduled, timezone: 'UTC', onrun: vi.fn() } })

    await fireEvent.input(screen.getByLabelText('Fire date'), { target: { value: '2020-01-01' } })
    await fireEvent.click(screen.getByText('Now'))

    const today = new Date().toISOString().slice(0, 10)
    expect(screen.getByLabelText('Fire date')).toHaveValue(today)
  })

  test('shows the command with its arguments as it will be sent', async () => {
    const { container } = render(SkillInvocation, { props: { skill: commanded, onrun: vi.fn() } })

    expect(container.querySelector('.outgoing-body')).toHaveTextContent('/triage')
    await fireEvent.input(screen.getByPlaceholderText(/optional arguments/), {
      target: { value: 'unread only' },
    })
    expect(container.querySelector('.outgoing-body')).toHaveTextContent('/triage unread only')
  })

  test('emits the schedule invocation without a message', async () => {
    const onrun = vi.fn()
    render(SkillInvocation, { props: { skill: scheduled, onrun } })

    await fireEvent.click(screen.getByText('Run'))
    expect(onrun).toHaveBeenCalledTimes(1)
    const arg = onrun.mock.calls[0][0]
    expect(arg.mode).toBe('schedule')
    expect(arg.message).toBeUndefined()
  })

  test('emits the command invocation with args', async () => {
    const onrun = vi.fn()
    render(SkillInvocation, { props: { skill: commanded, onrun } })

    await fireEvent.input(screen.getByPlaceholderText(/optional arguments/), {
      target: { value: 'unread' },
    })
    await fireEvent.click(screen.getByText('Run'))
    expect(onrun).toHaveBeenCalledWith(expect.objectContaining({ mode: 'command', args: 'unread' }))
  })

  test('message mode requires a message before it can run', async () => {
    const onrun = vi.fn()
    render(SkillInvocation, { props: { skill: scheduled, onrun } })

    await fireEvent.click(screen.getByText('As a chat message'))
    expect(screen.getByText('Run')).toBeDisabled()

    await fireEvent.input(screen.getByPlaceholderText(/summarise what happened/), {
      target: { value: 'what happened yesterday' },
    })
    expect(screen.getByText('Run')).not.toBeDisabled()
    await fireEvent.click(screen.getByText('Run'))
    expect(onrun).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'message', message: 'what happened yesterday',
    }))
  })

  test('switching modes changes what will be sent', async () => {
    const { container } = render(SkillInvocation, { props: { skill: commanded, onrun: vi.fn() } })
    expect(container.querySelector('.outgoing-body')).toHaveTextContent('/triage')

    await fireEvent.click(screen.getByText('On a schedule'))
    expect(container.querySelector('.outgoing-body').textContent).toMatch(/^\[Scheduled: inbox-triage \|/)
  })
})
