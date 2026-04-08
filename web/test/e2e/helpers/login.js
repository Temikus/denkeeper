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
    // The SPA uses hash routing — after login, the Login component is replaced
    // by the shell with a nav bar. Wait for the nav to appear instead of a URL change.
    await this.page.locator('nav').waitFor({ state: 'visible', timeout: 10000 })
  }

  async isLoggedIn() {
    // Check for nav or a page element that only shows when logged in.
    return this.page.locator('nav').isVisible()
  }
}
