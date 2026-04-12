// @ts-check
import { test, expect } from '@playwright/test'
import { LoginPage } from './helpers/login.js'
import { SchedulesPage } from './helpers/schedules.js'

test.describe('Schedules CRUD via UI', () => {
  test.beforeEach(async ({ page }) => {
    const login = new LoginPage(page)
    await login.goto()
    await login.loginWithPassword('test')
  })

  test('create, edit, and delete a schedule', async ({ page }) => {
    const schedules = new SchedulesPage(page)
    await schedules.goto()

    // Create a new schedule.
    await schedules.clickAdd()
    await schedules.fillForm({
      name: 'e2e-test-sched',
      expression: '@daily',
      channel: 'api:test',
    })
    await schedules.save()

    // Verify the row appears.
    const row = schedules.row('e2e-test-sched')
    await expect(row).toBeVisible()
    await expect(row.locator('.expr').first()).toContainText('@daily')

    // Edit: change the expression.
    await schedules.editRow('e2e-test-sched')
    const form = page.locator('[data-testid="schedule-form"]')
    const exprInput = form.locator('input[placeholder*="@daily"]')
    await exprInput.clear()
    await exprInput.fill('@hourly')
    await schedules.save()

    // Verify updated expression.
    await expect(row.locator('.expr').first()).toContainText('@hourly')

    // Delete.
    await schedules.deleteRow('e2e-test-sched')
    await schedules.confirmDelete()

    // Verify row is gone.
    await expect(row).not.toBeVisible()
  })
})
