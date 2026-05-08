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
    // by the shell with a nav bar. At narrow viewports the sidebar nav (nav.nav)
    // is hidden and only the bottom nav (nav.bottom-nav) is visible; at wide
    // viewports it is the reverse. Wait for whichever one becomes visible so
    // this helper works at any viewport width.
    await this.page.locator('nav.nav:visible, nav.bottom-nav:visible').first().waitFor({ state: 'visible', timeout: 10000 })
  }

  async isLoggedIn() {
    // Check for nav or a page element that only shows when logged in.
    // Use :visible so we match whichever nav is visible at the current viewport
    // rather than relying on DOM order (which would pick the hidden sidebar nav
    // at narrow mobile viewports).
    return this.page.locator('nav.nav:visible, nav.bottom-nav:visible').first().isVisible()
  }
}
