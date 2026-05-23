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

  test('edit form shows agent pill instead of disabled input', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText('Edit Skill')).toBeInTheDocument()
    })

    // Agent pill should show the agent name
    const pill = document.querySelector('.agent-pill')
    expect(pill).toBeInTheDocument()
    expect(pill.textContent.trim()).toContain('default')

    // No disabled agent input should be present
    const disabledInputs = document.querySelectorAll('input[disabled]')
    expect(disabledInputs.length).toBe(0)
  })

  test('edit form shows scope hint', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(screen.getByText(/Skills are scoped to one agent/)).toBeInTheDocument()
    })
  })

  test('edit form shows skill name as subtitle', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('Edit')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Edit'))
    await waitFor(() => {
      expect(document.querySelector('.form-subtitle')).toBeInTheDocument()
      expect(document.querySelector('.form-subtitle').textContent).toBe('greeting')
    })
  })

  test('add form shows agent dropdown not pill', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('+ Add Skill')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('+ Add Skill'))
    await waitFor(() => {
      expect(screen.getByText('Add Skill', { selector: 'h2' })).toBeInTheDocument()
    })

    // Agent dropdown should be present in add mode
    expect(document.querySelector('select')).toBeInTheDocument()
    // No agent pill in add mode
    expect(document.querySelector('.agent-pill')).not.toBeInTheDocument()
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

  test('test button disabled when skill has no command trigger', async () => {
    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('greeting')).toBeInTheDocument()
    })

    const testBtn = screen.getByText('Test')
    expect(testBtn).toBeDisabled()
    expect(testBtn.title).toBe('No command trigger')
  })

  test('test button enabled when skill has command trigger', async () => {
    server.use(
      http.get('/api/v1/skills', () => HttpResponse.json([
        { name: 'briefing', agent: 'default', triggers: ['command:briefing'], description: 'Daily brief' },
      ]))
    )

    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('briefing')).toBeInTheDocument()
    })

    const testBtn = screen.getByText('Test')
    expect(testBtn).not.toBeDisabled()
    expect(testBtn.title).toBe('Send /briefing in chat')
  })

  test('test button navigates to chat with pending skill test', async () => {
    const { pendingSkillTest } = await import('../../chatStore.js')
    const { get } = await import('svelte/store')

    server.use(
      http.get('/api/v1/skills', () => HttpResponse.json([
        { name: 'report', agent: 'helper', triggers: ['command:report'], description: 'Generate report' },
      ]))
    )

    render(Skills)
    await waitFor(() => {
      expect(screen.getByText('report')).toBeInTheDocument()
    })

    await fireEvent.click(screen.getByText('Test'))

    const pending = get(pendingSkillTest)
    expect(pending).toEqual({ agent: 'helper', command: '/report' })
    expect(window.location.hash).toBe('#/chat')

    // Cleanup
    pendingSkillTest.set(null)
  })
})
