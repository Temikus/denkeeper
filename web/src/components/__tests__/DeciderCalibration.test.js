import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import DeciderCalibration from '../DeciderCalibration.svelte'

const review = (id, min, supervisor, extra = {}) => ({
  audit_id: id, time: new Date(Date.now() - 2 * 86400000).toISOString(), conversation_id: 'c1',
  tool: 'web_fetch', arguments: '{"url":"https://paste.rs"}', model: 'typesafe/jev-1.13',
  scores: {}, lowest: 'safe_args', min_score: min, decider_cost: 0.0001,
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
})

describe('DeciderCalibration', () => {
  test('shows what the decider would have settled at the default thresholds', async () => {
    const query = serveReviews([
      review(1, 0.99, 'APPROVE'),
      review(2, 0.97, 'DENY', { tool: 'kv_set' }),
      review(3, 0.5, 'APPROVE'),
      review(4, 0.02, 'APPROVE', { tool: 'run_javascript', lowest: 'aligned' }),
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
    expect(rows[0].textContent).toContain('safe 0.97')
    expect(rows[1].textContent).toContain('aligned 0.02')
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
