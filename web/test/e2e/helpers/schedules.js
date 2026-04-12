// @ts-check

/** Page object for the Schedules page. */
export class SchedulesPage {
  /** @param {import('@playwright/test').Page} page */
  constructor(page) {
    this.page = page
  }

  async goto() {
    await this.page.goto('/#/schedules')
    await this.page.locator('.page-title').waitFor({ state: 'visible', timeout: 10000 })
  }

  /** Click the "+ Add Schedule" button to open the form. */
  async clickAdd() {
    await this.page.locator('[data-testid="add-schedule-btn"]').click()
    await this.page.locator('[data-testid="schedule-form"]').waitFor({ state: 'visible' })
  }

  /**
   * Fill the schedule form fields.
   * @param {{ name?: string, expression?: string, channel?: string, agent?: string }} fields
   */
  async fillForm({ name, expression, channel, agent }) {
    const form = this.page.locator('[data-testid="schedule-form"]')
    if (name) {
      await form.locator('input[placeholder*="daily-report"]').fill(name)
    }
    if (expression) {
      await form.locator('input[placeholder*="@daily"]').fill(expression)
    }
    if (channel) {
      await form.locator('input[placeholder*="adapter:externalID"]').fill(channel)
    }
    if (agent) {
      await form.locator('input[placeholder="default"]').fill(agent)
    }
  }

  /** Click the save button (Add Schedule or Update). */
  async save() {
    const form = this.page.locator('[data-testid="schedule-form"]')
    await form.locator('button.btn-primary').click()
    // Wait for form to close.
    await this.page.locator('[data-testid="schedule-form"]').waitFor({ state: 'hidden', timeout: 10000 })
  }

  /** Get a table row by schedule name. */
  row(name) {
    return this.page.locator(`[data-testid="schedule-row-${name}"]`)
  }

  /** Click the Edit button on a schedule row. */
  async editRow(name) {
    await this.row(name).locator('button:has-text("Edit")').click()
    await this.page.locator('[data-testid="schedule-form"]').waitFor({ state: 'visible' })
  }

  /** Click the Delete button on a schedule row. */
  async deleteRow(name) {
    await this.row(name).locator('button:has-text("Delete")').click()
    await this.page.locator('[data-testid="delete-confirm"]').waitFor({ state: 'visible' })
  }

  /** Confirm deletion in the modal. */
  async confirmDelete() {
    await this.page.locator('[data-testid="delete-confirm"] button.btn-danger').click()
    await this.page.locator('[data-testid="delete-confirm"]').waitFor({ state: 'hidden', timeout: 10000 })
  }
}
