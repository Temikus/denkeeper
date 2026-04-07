// @ts-check

/** Page object for navigation. */
export class NavHelper {
  /** @param {import('@playwright/test').Page} page */
  constructor(page) {
    this.page = page
  }

  /** Navigate to a page by clicking its nav link. */
  async goto(pageName) {
    await this.page.click(`nav a:has-text("${pageName}")`)
    // Wait for navigation to settle.
    await this.page.waitForLoadState('networkidle')
  }

  /** Get the currently active nav item text. */
  async currentPage() {
    const active = this.page.locator('nav a.active, nav a[aria-current="page"]')
    if (await active.count() > 0) {
      return active.first().textContent()
    }
    return null
  }

  /** Toggle dark/light theme if a toggle exists. */
  async toggleTheme() {
    const toggle = this.page.locator('[data-testid="theme-toggle"], button:has-text("theme"), .theme-toggle')
    if (await toggle.count() > 0) {
      await toggle.first().click()
    }
  }
}
