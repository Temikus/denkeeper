import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Schedules from '../../pages/Schedules.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Schedules page', () => {
  test('renders page title and add button', async () => {
    render(Schedules)
    expect(screen.getByText('Schedules')).toBeInTheDocument()
    expect(screen.getByText('+ Add Schedule')).toBeInTheDocument()
  })

  test('renders schedule table with data', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'daily-check', expression: '0 9 * * *', skill: 'report', channel: 'telegram:123', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('daily-check')).toBeInTheDocument()
      expect(screen.getByText('0 9 * * *')).toBeInTheDocument()
      expect(screen.getByText('yes')).toBeInTheDocument()
    })
  })

  test('shows empty state when no schedules', async () => {
    server.use(
      http.get('/api/v1/schedules', () => HttpResponse.json([]))
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText(/No schedules configured/)).toBeInTheDocument()
    })
  })

  test('add button opens inline form', async () => {
    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('+ Add Schedule')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Schedule'))
    await waitFor(() => {
      expect(screen.getByText('Add Schedule', { selector: 'h2' })).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/daily-report/)).toBeInTheDocument()
      expect(screen.getByPlaceholderText(/@daily/)).toBeInTheDocument()
    })
  })

  test('edit button opens pre-filled form', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'daily-check', expression: '0 9 * * *', channel: 'telegram:123', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText('Edit Schedule')).toBeInTheDocument()
    })
  })

  test('delete shows confirmation modal', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json([
          { name: 'daily-check', expression: '0 9 * * *', channel: 'telegram:123', enabled: true },
        ])
      )
    )

    render(Schedules)
    await waitFor(() => {
      // Find the delete button in the actions column
      const deleteBtn = document.querySelector('.btn-sm.danger')
      expect(deleteBtn).toBeInTheDocument()
    })

    const deleteBtn = document.querySelector('.btn-sm.danger')
    await fireEvent.click(deleteBtn)
    await waitFor(() => {
      expect(screen.getByText('Delete Schedule')).toBeInTheDocument()
    })
    const modal = document.querySelector('.confirm-modal')
    expect(modal.textContent).toContain('daily-check')
  })

  test('error state shows ErrorBanner', async () => {
    server.use(
      http.get('/api/v1/schedules', () =>
        HttpResponse.json({ error: 'Server error' }, { status: 500 })
      )
    )

    render(Schedules)
    await waitFor(() => {
      expect(screen.getByText('Server error')).toBeInTheDocument()
    })
  })
})
