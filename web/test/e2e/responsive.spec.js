// @ts-check
import { test, expect } from '@playwright/test'
import { LoginPage } from './helpers/login.js'

test.describe('Responsive layout', () => {
  test('renders at 320px mobile viewport', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 568 })
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')

    // Page should render without horizontal overflow.
    const body = page.locator('body')
    await expect(body).toBeVisible()

    // Check that content fits within viewport width.
    const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(scrollWidth).toBeLessThanOrEqual(330) // small tolerance
  })

  test('renders at 1920px desktop viewport', async ({ page }) => {
    await page.setViewportSize({ width: 1920, height: 1080 })
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')

    // Sidebar nav should be visible on desktop (bottom nav is hidden via CSS at this width).
    await expect(page.locator('nav.nav')).toBeVisible()
  })
})
