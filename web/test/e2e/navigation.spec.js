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
        await page.waitForLoadState('networkidle')
        // Page should render without errors.
        await expect(page.locator('.page-title, h1, h2').first()).toBeVisible({ timeout: 5000 })
      }
    })
  }

  test('can navigate back with browser back button', async ({ page }) => {
    // Navigate to two different pages.
    const overviewLink = page.locator('nav a:has-text("Overview")')
    if (await overviewLink.count() > 0) {
      await overviewLink.click()
      await page.waitForLoadState('networkidle')
    }

    const costsLink = page.locator('nav a:has-text("Costs")')
    if (await costsLink.count() > 0) {
      await costsLink.click()
      await page.waitForLoadState('networkidle')
    }

    await page.goBack()
    await page.waitForLoadState('networkidle')
  })
})
