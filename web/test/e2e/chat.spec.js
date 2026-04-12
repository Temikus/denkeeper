// @ts-check
import { test, expect } from '@playwright/test'
import { LoginPage } from './helpers/login.js'
import { ChatPage } from './helpers/chat.js'

test.describe('Chat conversation', () => {
  test.beforeEach(async ({ page }) => {
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')
  })

  test('can send a message and receive a streamed response', async ({ page }) => {
    const chat = new ChatPage(page)
    await chat.goto()

    // Verify chat page loaded.
    expect(await chat.isReady()).toBe(true)

    // Agent selector should be populated.
    await expect(chat.agentSelector()).toBeVisible()

    // Send a message.
    await chat.sendMessage('Hello, E2E test!')

    // Wait for assistant response.
    await chat.waitForResponse()

    // Verify the response contains text from the mock LLM.
    const lastMsg = await chat.lastAssistantMessage()
    expect(lastMsg).toBeTruthy()
    expect(lastMsg).toContain('Hello from E2E mock!')
  })

  test('creates a session after sending a message', async ({ page }) => {
    const chat = new ChatPage(page)
    await chat.goto()

    // Session selector should start with "New session".
    const sessionSel = chat.sessionSelector()
    await expect(sessionSel).toBeVisible()

    // Send a message to create a session.
    await chat.sendMessage('Create session test')
    await chat.waitForResponse()

    // After the response, the session selector should have an option beyond "New session".
    // Poll until the session list refreshes (driven by async loadSessions call).
    await expect(async () => {
      const optionCount = await sessionSel.locator('option').count()
      expect(optionCount).toBeGreaterThan(1)
    }).toPass({ timeout: 5000 })
  })

  test('shows empty state before any messages', async ({ page }) => {
    const chat = new ChatPage(page)
    await chat.goto()

    // Messages container should show the empty state.
    const empty = chat.messagesContainer().locator('.empty')
    await expect(empty).toBeVisible()
    await expect(empty).toContainText('Send a message')
  })
})
