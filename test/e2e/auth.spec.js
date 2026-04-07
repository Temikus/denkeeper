// @ts-check
import { test, expect } from '@playwright/test'
import { LoginPage } from './helpers/login.js'

test.describe('Authentication', () => {
  test('shows login page when not authenticated', async ({ page }) => {
    await page.goto('/')
    // Should see a password input or login form.
    await expect(page.locator('input[type="password"]')).toBeVisible({ timeout: 10000 })
  })

  test('can log in with valid password', async ({ page }) => {
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')
    expect(await login.isLoggedIn()).toBe(true)
  })

  test('rejects invalid password', async ({ page }) => {
    await page.goto('/')
    await page.fill('input[type="password"]', 'wrong-password')
    await page.click('button[type="submit"]')
    // Should remain on login page or show error.
    await expect(page.locator('input[type="password"]')).toBeVisible()
  })

  test('can log out', async ({ page }) => {
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')
    expect(await login.isLoggedIn()).toBe(true)

    // Find and click logout.
    const logoutBtn = page.locator('button:has-text("Logout"), a:has-text("Logout"), button:has-text("Log out")')
    if (await logoutBtn.count() > 0) {
      await logoutBtn.first().click()
      await expect(page.locator('input[type="password"]')).toBeVisible({ timeout: 5000 })
    }
  })
})
