// @ts-check
import { test, expect } from '@playwright/test'
import { LoginPage } from './helpers/login.js'

// Real-world names and full metadata: the metadata column is what crowded
// the name out when the selector was narrow.
const MODELS = [
  ['deepseek/deepseek-v3.2-exp', 'DeepSeek: DeepSeek V3.2 Exp', 38_619_000_000_000],
  ['z-ai/glm-5.3-flash', 'Z.ai: GLM 5.3 Flash', 11_574_700_000_000],
  ['xiaomi/mimo-v2-flash', 'Xiaomi: MiMo-V2-Flash', 11_252_900_000_000],
  ['tencent/hunyuan-a13b-instruct', 'Tencent: Hunyuan A13B Instruct', 8_283_900_000_000],
].map(([id, name, weekly_tokens]) => ({
  id, name, weekly_tokens,
  provider: 'openrouter',
  input_per_mtok: 0.3,
  output_per_mtok: 1.2,
  supports_tools: true,
}))

async function openSelector(page) {
  await page.route('**/api/v1/models/details*', route => route.fulfill({ json: { models: MODELS } }))
  const login = new LoginPage(page)
  await login.goto()
  await login.loginWithPassword('test')
  await page.goto('/#/providers')
  await page.locator('.card-actions button', { hasText: 'Edit' }).first().click()
  // Pin a provider so the selector loads models on focus.
  await page.locator('#default-provider').selectOption({ index: 1 })
  await page.locator('.model-selector .selector-input').click()
  await expect(page.locator('.model-option')).toHaveCount(MODELS.length)
}

// Each name must render in full: no ellipsis and no clipping by the dropdown.
async function expectNamesReadable(page) {
  const names = page.locator('.model-option-name')
  for (let i = 0; i < MODELS.length; i++) {
    const name = names.nth(i)
    await expect(name).toHaveText(MODELS[i].name)
    const truncated = await name.evaluate(el => el.scrollWidth > el.clientWidth + 1)
    expect(truncated, `"${MODELS[i].name}" is truncated`).toBe(false)
  }
  const overflow = await page.locator('.model-list').evaluate(el => el.scrollWidth > el.clientWidth + 1)
  expect(overflow, 'model list scrolls horizontally').toBe(false)
}

test.describe('Model selector', () => {
  test('shows full model names at phone width', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openSelector(page)
    await expectNamesReadable(page)
  })

  test('shows full model names at desktop width', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await openSelector(page)
    await expectNamesReadable(page)
  })
})
