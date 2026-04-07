// @ts-check

/** Page object for the login page. */
export class LoginPage {
  /** @param {import('@playwright/test').Page} page */
  constructor(page) {
    this.page = page
  }

  async goto() {
    await this.page.goto('/')
  }

  async loginWithPassword(password) {
    await this.page.fill('input[type="password"]', password)
    await this.page.click('button[type="submit"]')
    await this.page.waitForURL(/.*(?:overview|chat).*/, { timeout: 10000 })
  }

  async isLoggedIn() {
    // Check for nav or a page element that only shows when logged in.
    return this.page.locator('nav').isVisible()
  }
}
