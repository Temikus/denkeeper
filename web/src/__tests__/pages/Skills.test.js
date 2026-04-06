import { describe, test, expect, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/svelte'
import { http, HttpResponse } from 'msw'
import { server } from '../../test/server.js'
import { token, authMode } from '../../store.js'
import Skills from '../../pages/Skills.svelte'

beforeEach(() => {
  token.set('test-key')
  authMode.set('token')
})

describe('Skills page', () => {
  test('renders page title and add button', async () => {
    render(Skills)
    expect(screen.getByText('Skills')).toBeInTheDocument()
    expect(screen.getByText('+ Add Skill')).toBeInTheDocument()
  })

  test('renders skill table with data', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('greeting')).toBeInTheDocument()
      expect(screen.getByText('hello, hi')).toBeInTheDocument()
    })
  })

  test('shows empty state when no skills', async () => {
    server.use(
      http.get('/api/v1/skills', () => HttpResponse.json([]))
    )

    render(Skills)
    await waitFor(() => {
      expect(screen.getByText(/No skills loaded/)).toBeInTheDocument()
    })
  })

  test('filter narrows visible skills', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('greeting')).toBeInTheDocument()
    })

    const filterInput = screen.getByPlaceholderText(/Filter by name/)
    await fireEvent.input(filterInput, { target: { value: 'nonexistent' } })

    await waitFor(() => {
      expect(screen.getByText(/No matching skills/)).toBeInTheDocument()
    })
  })

  test('shows count indicator', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('1 of 1')).toBeInTheDocument()
    })
  })

  test('add button opens form', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('+ Add Skill')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Skill'))
    await waitFor(() => {
      expect(screen.getByText('Add Skill', { selector: 'h2' })).toBeInTheDocument()
    })
  })

  test('edit button opens form', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText('Edit Skill')).toBeInTheDocument()
    })
  })

  test('delete shows confirmation', async () => {
    render(Skills)
    await waitFor(() => {
      const deleteBtn = document.querySelector('.btn-sm.danger')
      expect(deleteBtn).toBeInTheDocument()
    })

    const deleteBtn = document.querySelector('.btn-sm.danger')
    await fireEvent.click(deleteBtn)
    await waitFor(() => {
      expect(screen.getByText('Delete Skill')).toBeInTheDocument()
    })
    // The confirmation modal should mention the skill name
    const modal = document.querySelector('.confirm-modal')
    expect(modal.textContent).toContain('greeting')
  })
})
