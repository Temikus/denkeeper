import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import DeciderCalibration from '../DeciderCalibration.svelte'
import { attention } from '../../attention.js'

const review = (id, min, supervisor, extra = {}) => ({
  audit_id: id, time: new Date(Date.now() - 2 * 86400000).toISOString(), conversation_id: 'c1',
  tool: 'web_fetch', arguments: '{"url":"https://paste.rs"}', model: 'typesafe/jev-1.13',
  scores: {}, lowest: 'no_secrets', min_score: min, decider_cost: 0.0001,
  supervisor, supervisor_name: 'argus', supervisor_cost: 0.04, ...extra,
})

function serveReviews(reviews, extra = {}) {
  let query = null
  server.use(http.get('/api/v1/agents/:name/decider-reviews', ({ request }) => {
    query = new URL(request.url).searchParams
    return HttpResponse.json({ agent: 'pamela', decider: 'jev', supervisor: 'argus', reviews, failed: 0, truncated: false, ...extra })
  }))
  return () => query
}

const props = { agent: 'pamela', decider: 'jev', supervisor: 'argus', approveAt: '', denyAt: '' }

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
  attention.set({ pendingApprovals: 0, unhealthyTools: [], deciderReviews: {} })
})

describe('DeciderCalibration', () => {
  test('shows what the decider would have settled at the default thresholds', async () => {
    const query = serveReviews([
      review(1, 0.99, 'APPROVE'),
      review(2, 0.97, 'DENY', { tool: 'kv_set' }),
      review(3, 0.5, 'APPROVE'),
      review(4, 0.02, 'APPROVE', { tool: 'run_javascript', lowest: 'no_injection' }),
    ])
    render(DeciderCalibration, { props })

    await waitFor(() => screen.getByTestId('calibration-stats'))
    expect(query().get('decider')).toBe('jev')
    expect(query().get('since')).toBeTruthy()
    expect(screen.getByText('4 shadow reviews of pamela\'s tool calls, compared with argus\'s verdicts')).toBeInTheDocument()
    expect(screen.getByText('75%')).toBeInTheDocument()
    expect(screen.getByTestId('calibration-unsafe').textContent).toBe('1')
    expect(screen.getByText('$0.12')).toBeInTheDocument()
    expect(screen.getByText(/Escalate to argus · 1 call/)).toBeInTheDocument()

    const rows = screen.getByTestId('calibration-disagreements').querySelectorAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].textContent).toContain('jev approved')
    expect(rows[0].textContent).toContain('argus denied')
    expect(rows[0].textContent).toContain('no secrets 0.97')
    expect(rows[1].textContent).toContain('no injection 0.02')
  })

  test('using the suggested threshold recomputes the panel', async () => {
    serveReviews([review(1, 0.97, 'DENY'), review(2, 0.99, 'APPROVE')])
    render(DeciderCalibration, { props })

    await waitFor(() => screen.getByTestId('calibration-suggestion'))
    expect(screen.getByTestId('calibration-suggestion').textContent).toContain('Approve at 0.98 would remove the unsafe approval')
    await fireEvent.click(screen.getByText('Use 0.98'))

    await waitFor(() => expect(screen.getByTestId('calibration-unsafe').textContent).toBe('0'))
    expect(screen.queryByTestId('calibration-suggestion')).toBeNull()
  })

  test('arrow keys on a threshold handle move it in 0.01 steps', async () => {
    serveReviews([review(1, 0.94, 'APPROVE')])
    render(DeciderCalibration, { props })

    const approve = await waitFor(() => screen.getByRole('slider', { name: 'Approve threshold' }))
    expect(approve).toHaveAttribute('aria-valuetext', '0.95')
    expect(screen.getByText('0%')).toBeInTheDocument()

    await fireEvent.keyDown(approve, { key: 'ArrowLeft' })
    expect(approve).toHaveAttribute('aria-valuetext', '0.94')
    expect(screen.getByText('100%')).toBeInTheDocument()

    // The deny handle cannot cross the approve one.
    const deny = screen.getByRole('slider', { name: 'Deny threshold' })
    for (let i = 0; i < 120; i++) await fireEvent.keyDown(deny, { key: 'ArrowRight' })
    expect(deny).toHaveAttribute('aria-valuetext', '0.93')
  })

  test('Home and End jump a handle to its limits', async () => {
    serveReviews([review(1, 0.5, 'APPROVE')])
    render(DeciderCalibration, { props })

    const approve = await waitFor(() => screen.getByRole('slider', { name: 'Approve threshold' }))
    await fireEvent.keyDown(approve, { key: 'End' })
    expect(approve).toHaveAttribute('aria-valuetext', '0.99')
    await fireEvent.keyDown(approve, { key: 'Home' })
    expect(approve).toHaveAttribute('aria-valuetext', '0.06')
  })

  test('the approve handle stays put when a typed deny leaves it no room', async () => {
    serveReviews([review(1, 0.5, 'APPROVE')])
    render(DeciderCalibration, { props: { ...props, approveAt: 0.3, denyAt: 0.995 } })

    const approve = await waitFor(() => screen.getByRole('slider', { name: 'Approve threshold' }))
    await fireEvent.keyDown(approve, { key: 'ArrowRight' })
    expect(approve).toHaveAttribute('aria-valuetext', '0.30')
  })

  test('says how to get reviews when there are none', async () => {
    serveReviews([])
    render(DeciderCalibration, { props: { ...props, saved: false } })
    await waitFor(() => expect(screen.getByTestId('calibration-empty').textContent).toContain('Save jev in shadow mode'))
  })

  test('marks spend avoided as unknown for reviews audited without a supervisor cost', async () => {
    serveReviews([review(1, 0.99, 'APPROVE', { supervisor_cost: null })])
    render(DeciderCalibration, { props })
    await waitFor(() => expect(screen.getByText(/argus cost not recorded yet/)).toBeInTheDocument())
  })

  test('shows a load failure inline', async () => {
    server.use(http.get('/api/v1/agents/:name/decider-reviews', () => HttpResponse.json({ error: 'audit not configured' }, { status: 503 })))
    render(DeciderCalibration, { props })
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('audit not configured'))
  })

  test('stays hidden without audit:read', async () => {
    server.use(http.get('/api/v1/agents/:name/decider-reviews', () => HttpResponse.json({ error: 'insufficient scope' }, { status: 403 })))
    const { container } = render(DeciderCalibration, { props })
    await waitFor(() => expect(container.querySelector('#decider-calibration')).toBeNull())
  })
})

describe('DeciderCalibration review mark', () => {
  const MARK = '2026-11-21T00:00:00Z'
  const NEW_TIME = '2026-11-22T10:00:00.123456789Z'
  const OLD_TIME = '2026-11-19T10:00:00Z'
  // Unsafe approvals: jev approves (0.99), argus denies.
  const fresh = () => review(1, 0.99, 'DENY', { time: NEW_TIME, tool: 'kv_set' })
  const old = () => review(2, 0.99, 'DENY', { time: OLD_TIME, tool: 'web_search' })
  const tableRows = () => [...screen.getByTestId('calibration-disagreements').querySelectorAll('tbody tr')]

  function serveMark(handler) {
    const bodies = []
    server.use(http.post('/api/v1/agents/:name/decider-reviews/reviewed', async ({ request }) => {
      const body = await request.json()
      bodies.push(body)
      return handler(body)
    }))
    return bodies
  }

  test('lists fresh rows above a divider that shows when and who', async () => {
    serveReviews([old(), fresh()], { reviewed_up_to: MARK, reviewed_by: 'temik' })
    render(DeciderCalibration, { props })

    await waitFor(() => screen.getByTestId('calibration-mark'))
    const rows = tableRows()
    expect(rows[0]).toHaveTextContent('kv_set')
    expect(rows[1]).toHaveAttribute('data-testid', 'calibration-mark')
    expect(rows[1]).toHaveTextContent(/Reviewed up to .* · temik/)
    expect(rows[1].querySelector('[title]').getAttribute('title')).toBe('2026-11-21T00:00:00.000Z')
    expect(rows[2]).toHaveTextContent('web_search')
    expect(rows[2]).toHaveClass('reviewed')
    expect(screen.getByTestId('calibration-new')).toHaveTextContent('1 new since')
  })

  test('shows no divider before the first mark', async () => {
    serveReviews([old(), fresh()])
    render(DeciderCalibration, { props })

    await waitFor(() => screen.getByTestId('calibration-disagreements'))
    expect(screen.queryByTestId('calibration-mark')).toBeNull()
    expect(screen.getByRole('button', { name: 'Mark all reviewed' })).toBeInTheDocument()
  })

  test('has no button when nothing fresh is an unsafe approval', async () => {
    // The only fresh disagreement is a wrong denial, which the badge does not count.
    serveReviews([old(), review(3, 0.01, 'APPROVE', { time: NEW_TIME })], { reviewed_up_to: MARK, reviewed_by: 'temik' })
    render(DeciderCalibration, { props })

    await waitFor(() => screen.getByTestId('calibration-disagreements'))
    expect(screen.queryByRole('button', { name: 'Mark all reviewed' })).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent('Nothing new to review')
  })

  test('posts the newest settled time unchanged and shows Marking… while in flight', async () => {
    serveReviews([old(), fresh()], { reviewed_up_to: MARK, reviewed_by: 'temik' })
    let release
    const bodies = serveMark(async body => {
      await new Promise(r => { release = r })
      return HttpResponse.json({ agent: 'pamela', decider: 'jev', reviewed_up_to: body.up_to, reviewed_by: 'temik' })
    })
    render(DeciderCalibration, { props })

    await fireEvent.click(await waitFor(() => screen.getByRole('button', { name: 'Mark all reviewed' })))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).toEqual({ decider: 'jev', up_to: NEW_TIME })
    expect(screen.getByRole('button', { name: 'Marking…' })).toBeDisabled()
    release()
    await waitFor(() => expect(screen.queryByRole('button', { name: /Mark/ })).toBeNull())
  })

  test('a failed mark shows the error inline and brings the button back', async () => {
    serveReviews([old(), fresh()], { reviewed_up_to: MARK, reviewed_by: 'temik' })
    serveMark(() => HttpResponse.json({ error: 'up_to is in the future' }, { status: 400 }))
    render(DeciderCalibration, { props })

    await fireEvent.click(await waitFor(() => screen.getByRole('button', { name: 'Mark all reviewed' })))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Could not mark as reviewed: up_to is in the future'))
    expect(screen.getByRole('button', { name: 'Mark all reviewed' })).toBeEnabled()
  })

  test('after a mark the divider moves, nothing is new, and the badge count is fetched again', async () => {
    serveReviews([old(), fresh()], { reviewed_up_to: MARK, reviewed_by: 'temik' })
    serveMark(body => HttpResponse.json({ agent: 'pamela', decider: 'jev', reviewed_up_to: body.up_to, reviewed_by: 'temik' }))
    let counts = 0
    server.use(http.get('/api/v1/decider-reviews/unreviewed', () => { counts++; return HttpResponse.json({ pamela: 0 }) }))
    render(DeciderCalibration, { props })

    await fireEvent.click(await waitFor(() => screen.getByRole('button', { name: 'Mark all reviewed' })))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Nothing new to review'))
    // The button is gone; focus lands on the status text, not <body>.
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('status')))
    const rows = tableRows()
    expect(rows[0]).toHaveAttribute('data-testid', 'calibration-mark')
    expect(rows[1]).toHaveClass('reviewed')
    expect(screen.queryByTestId('calibration-new')).toBeNull()
    await waitFor(() => expect(counts).toBe(1))
  })

  test('says it is waiting when a review still lacks a verdict before a newer disagreement', async () => {
    const ago = ms => new Date(Date.now() - ms).toISOString()
    // The mark already sits on the last settled review; a younger one has no
    // verdict yet, so the newer disagreement cannot be marked past it.
    const settledAt = ago(20 * 60000)
    serveReviews([
      review(1, 0.99, 'DENY', { time: settledAt }),
      review(2, 0.5, undefined, { time: ago(5 * 60000), supervisor_name: '' }),
      review(3, 0.99, 'DENY', { time: ago(60000), tool: 'kv_set' }),
    ], { reviewed_up_to: settledAt, reviewed_by: 'temik' })
    render(DeciderCalibration, { props })

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Waiting on the supervisor for newer calls'))
    expect(screen.queryByRole('button', { name: 'Mark all reviewed' })).toBeNull()
    expect(screen.getByTestId('calibration-new')).toHaveTextContent('1 new since')
  })

  test('the head follows the saved-threshold count while the row keeps its dot', async () => {
    attention.set({ pendingApprovals: 0, unhealthyTools: [], deciderReviews: { pamela: 0 } })
    serveReviews([old(), fresh()], { reviewed_up_to: MARK, reviewed_by: 'temik' })
    render(DeciderCalibration, { props })

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Nothing new to review'))
    expect(screen.queryByRole('button', { name: 'Mark all reviewed' })).toBeNull()
    expect(screen.queryByTestId('calibration-new')).toBeNull()
    expect(tableRows()[0].querySelector('.new-dot')).not.toBeNull()
  })

  test('an unsaved decider falls back to the slider count', async () => {
    attention.set({ pendingApprovals: 0, unhealthyTools: [], deciderReviews: { pamela: 0 } })
    serveReviews([old(), fresh()], { reviewed_up_to: MARK, reviewed_by: 'temik' })
    render(DeciderCalibration, { props: { ...props, saved: false } })

    await waitFor(() => expect(screen.getByRole('button', { name: 'Mark all reviewed' })).toBeInTheDocument())
  })

  test('a truncated load cannot be marked and says why', async () => {
    serveReviews([old(), fresh()], { reviewed_up_to: MARK, reviewed_by: 'temik', truncated: true })
    render(DeciderCalibration, { props })

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Pick a shorter time range'))
    expect(screen.queryByRole('button', { name: 'Mark all reviewed' })).toBeNull()
  })
})
