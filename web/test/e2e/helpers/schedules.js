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
    // Wait for the inline panel to expand (has .open class).
    await this.page.locator('.inline-panel.open').waitFor({ state: 'visible' })
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
      await this._selectOrCustom(form, '[data-testid="channel-select"]', 'input[placeholder="channel name"]', channel)
    }
    if (agent) {
      await this._selectOrCustom(form, '[data-testid="agent-select"]', 'input[placeholder="agent name"]', agent)
    }
  }

  /**
   * Select a value from a dropdown, or pick "Custom..." and type into the text input.
   * @param {import('@playwright/test').Locator} form
   * @param {string} selectSelector
   * @param {string} customInputSelector
   * @param {string} value
   */
  async _selectOrCustom(form, selectSelector, customInputSelector, value) {
    const select = form.locator(selectSelector)
    const options = await select.locator('option').evaluateAll(
      (opts) => opts.map(o => o.value).filter(v => v !== '__custom__')
    )
    if (options.includes(value)) {
      await select.selectOption(value)
    } else {
      await select.selectOption('__custom__')
      await form.locator(customInputSelector).fill(value)
    }
  }

  /** Click the save button (Add Schedule or Update). */
  async save() {
    const form = this.page.locator('[data-testid="schedule-form"]')
    const btn = form.locator('[data-testid="schedule-save-btn"]')
    // Click and wait for the schedule API response in parallel.
    const [response] = await Promise.all([
      this.page.waitForResponse(
        (resp) => resp.url().includes('/api/v1/schedules') && ['POST', 'PATCH'].includes(resp.request().method()),
        { timeout: 10000 },
      ),
      btn.click(),
    ])
    if (!response.ok()) {
      const body = await response.text()
      throw new Error(`Schedule save returned ${response.status()}: ${body}`)
    }
    // The inline panel collapses via CSS grid animation (loses .open class).
    await this.page.locator('.inline-panel.open').waitFor({ state: 'hidden', timeout: 15000 })
  }

  /** Get a table row by schedule name. */
  row(name) {
    return this.page.locator(`[data-testid="schedule-row-${name}"]`)
  }

  /** Click the Edit icon button on a schedule row. */
  async editRow(name) {
    await this.row(name).getByRole('button', { name: `Edit ${name}` }).click()
    await this.page.locator('.inline-panel.open').waitFor({ state: 'visible' })
  }

  /** Click the Delete icon button on a schedule row. */
  async deleteRow(name) {
    await this.row(name).getByRole('button', { name: `Delete ${name}` }).click()
    await this.page.locator('[data-testid="delete-confirm"]').waitFor({ state: 'visible' })
  }

  /** Confirm deletion in the modal. */
  async confirmDelete() {
    await this.page.locator('[data-testid="delete-confirm"] button.btn-danger').click()
    await this.page.locator('[data-testid="delete-confirm"]').waitFor({ state: 'hidden', timeout: 10000 })
  }
}
