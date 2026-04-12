// @ts-check
import { test, expect } from '@playwright/test'
import { LoginPage } from './helpers/login.js'

test.describe('Theme persistence', () => {
  test('toggles to dark mode and persists across reload', async ({ page }) => {
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')

    // Verify default theme is light (no .dark class on <html>).
    const html = page.locator('html')
    await expect(html).not.toHaveClass(/dark/)

    // Toggle to dark mode.
    await page.locator('[data-testid="theme-toggle"]').click()
    await expect(html).toHaveClass(/dark/)

    // Reload the page — session cookie persists, so we stay logged in.
    await page.reload()
    await page.locator('nav').waitFor({ state: 'visible', timeout: 10000 })

    // Theme should persist via localStorage.
    await expect(html).toHaveClass(/dark/)
  })

  test('toggles back to light mode', async ({ page }) => {
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')

    const html = page.locator('html')

    // Toggle to dark.
    await page.locator('[data-testid="theme-toggle"]').click()
    await expect(html).toHaveClass(/dark/)

    // Toggle back to light.
    await page.locator('[data-testid="theme-toggle"]').click()
    await expect(html).not.toHaveClass(/dark/)
  })
})
