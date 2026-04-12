// @ts-check
import { test, expect } from '@playwright/test'
import { LoginPage } from './helpers/login.js'

test.describe('Cross-page navigation', () => {
  test.beforeEach(async ({ page }) => {
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')
  })

  test('clicking pending approvals card navigates to Approvals page', async ({ page }) => {
    // Navigate to Overview.
    await page.click('nav a:has-text("Overview")')
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 5000 })

    // The pending approvals card should be visible.
    const card = page.locator('[data-testid="pending-approvals-card"]')
    await expect(card).toBeVisible()

    // Click the card.
    await card.click()

    // Should navigate to Approvals page.
    await expect(page).toHaveURL(/#\/approvals/)
    await expect(page.locator('.page-title, h1, h2').first()).toBeVisible()
  })

  test('navigating to Chat shows agent selector and input', async ({ page }) => {
    await page.click('nav a:has-text("Chat")')
    await expect(page.locator('[data-testid="chat-input"]')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('[data-testid="agent-selector"]')).toBeVisible()
  })

  test('navigating to Schedules shows the add button', async ({ page }) => {
    await page.click('nav a:has-text("Schedules")')
    await expect(page.locator('[data-testid="add-schedule-btn"]')).toBeVisible({ timeout: 5000 })
  })
})
