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
    // by the shell with a nav bar. Use .first() because both the sidebar nav and
    // the mobile bottom nav are present in the DOM; strict mode would reject the
    // bare 'nav' locator when both match.
    await this.page.locator('nav').first().waitFor({ state: 'visible', timeout: 10000 })
  }

  async isLoggedIn() {
    // Check for nav or a page element that only shows when logged in.
    // Use .first() so strict mode doesn't fire when both sidebar and bottom nav are present.
    return this.page.locator('nav').first().isVisible()
  }
}
