import { describe, test, expect, beforeEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Schedules from '../../pages/Schedules.svelte'

// Force the mobile layout: isMobile is a matchMedia-backed singleton created at
// module load, so the branch can only be exercised via a module mock.
vi.mock('../../store.js', async (importOriginal) => {
  const mod = await importOriginal()
  const { readable } = await import('svelte/store')
  return { ...mod, isMobile: readable(true) }
})

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

const rows = [
  {
    name: 'daily-check', expression: '0 9 * * *', skill: 'report', agent: 'default',
    channel: 'telegram:123', enabled: true,
    last_run: '2026-01-01T09:00:00Z', next_run: '2099-01-02T09:00:00Z',
  },
  { name: 'paused-job', expression: '@hourly', agent: 'default', channel: 'telegram:123', enabled: false },
]

function useSchedules(list = rows) {
  server.use(http.get('/api/v1/schedules', () => HttpResponse.json(list)))
}

describe('Schedules page (mobile)', () => {
  test('renders card cells instead of a table', async () => {
    useSchedules()
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('schedule-row-daily-check')).toBeInTheDocument()
    })
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    const cell = screen.getByTestId('schedule-row-daily-check')
    expect(within(cell).getByText('0 9 * * *')).toBeInTheDocument()
    expect(within(cell).getByText('skill: report')).toBeInTheDocument()
    expect(within(cell).getByLabelText('Toggle daily-check')).toBeChecked()
    // Channel and last-run are desktop-only columns; edit panel still has them.
    expect(within(cell).queryByText('telegram:123')).not.toBeInTheDocument()
  })

  test('section header shows a numeric count', async () => {
    useSchedules()
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('agent-section-default')).toBeInTheDocument()
    })
    const header = screen.getByTestId('agent-section-default').querySelector('.section-header')
    expect(header.textContent).not.toContain('schedules')
    expect(header.textContent).toContain('2')
  })

  test('paused cell shows Paused and the paused class', async () => {
    useSchedules()
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('schedule-row-paused-job')).toBeInTheDocument()
    })
    const cell = screen.getByTestId('schedule-row-paused-job')
    expect(cell).toHaveClass('paused')
    expect(within(cell).getByText('Paused')).toBeInTheDocument()
    expect(within(cell).getByLabelText('Toggle paused-job')).not.toBeChecked()
  })

  test('kebab Edit opens the form prefilled', async () => {
    useSchedules()
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('schedule-row-daily-check')).toBeInTheDocument()
    })
    const cell = screen.getByTestId('schedule-row-daily-check')
    await fireEvent.click(within(cell).getByTitle('More actions'))
    await fireEvent.click(within(cell).getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText('Edit Schedule')).toBeInTheDocument()
    })
    expect(screen.getByPlaceholderText('e.g. daily-report')).toHaveValue('daily-check')
  })

  test('kebab Delete opens the confirm modal', async () => {
    useSchedules()
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('schedule-row-daily-check')).toBeInTheDocument()
    })
    const cell = screen.getByTestId('schedule-row-daily-check')
    await fireEvent.click(within(cell).getByTitle('More actions'))
    await fireEvent.click(within(cell).getByText('Delete'))
    await waitFor(() => {
      expect(screen.getByTestId('delete-confirm')).toBeInTheDocument()
    })
    expect(screen.getByTestId('delete-confirm').textContent).toContain('daily-check')
  })

  test('toggle sends a partial enabled PATCH from a card cell', async () => {
    let patchBody = null
    useSchedules()
    server.use(
      http.patch('/api/v1/schedules/daily-check', async ({ request }) => {
        patchBody = await request.json()
        return HttpResponse.json({ status: 'ok' })
      })
    )
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('schedule-row-daily-check')).toBeInTheDocument()
    })
    await fireEvent.click(screen.getByLabelText('Toggle daily-check'))
    await waitFor(() => {
      expect(patchBody).toEqual({ enabled: false })
    })
  })

  test('opening edit scrolls the inline panel into view', async () => {
    // jsdom has no scrollIntoView; the component guards with ?. so a stub is enough.
    const scrollSpy = vi.fn()
    Element.prototype.scrollIntoView = scrollSpy
    useSchedules()
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByTestId('schedule-row-daily-check')).toBeInTheDocument()
    })
    const cell = screen.getByTestId('schedule-row-daily-check')
    await fireEvent.click(within(cell).getByTitle('More actions'))
    await fireEvent.click(within(cell).getByText('Edit'))
    await waitFor(() => {
      expect(scrollSpy).toHaveBeenCalled()
    })
    delete Element.prototype.scrollIntoView
  })

  test('add button renders as a compact FAB', async () => {
    render(Schedules)
    const btn = screen.getByTestId('add-schedule-btn')
    expect(btn).toHaveTextContent('+')
    expect(btn).not.toHaveTextContent('Add Schedule')
    expect(btn).toHaveClass('mobile-fab')
    expect(btn).toHaveAccessibleName('Add schedule')
  })
})
