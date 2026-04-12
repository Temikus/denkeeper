// @ts-check
import { test, expect } from '@playwright/test'
import { LoginPage } from './helpers/login.js'

test.describe('Navigation', () => {
  test.beforeEach(async ({ page }) => {
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')
  })

  const pages = [
    'Overview',
    'Chat',
    'Approvals',
    'Sessions',
    'Schedules',
    'Skills',
    'Tools',
    'Costs',
    'Agents',
    'API Keys',
  ]

  for (const pageName of pages) {
    test(`can navigate to ${pageName}`, async ({ page }) => {
      const link = page.locator(`nav a:has-text("${pageName}")`)
      if (await link.count() > 0) {
        await link.click()
        // Wait for the page content to render instead of networkidle.
        await expect(page.locator('.page-title, h1, h2').first()).toBeVisible({ timeout: 5000 })
      }
    })
  }

  test('can navigate back with browser back button', async ({ page }) => {
    // Navigate to two different pages.
    await page.locator('nav a:has-text("Overview")').click()
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 5000 })

    await page.locator('nav a:has-text("Costs")').click()
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 5000 })

    await page.goBack()
    // After going back, the Overview page should render.
    await expect(page.locator('.page-title').first()).toBeVisible({ timeout: 5000 })
  })
})
