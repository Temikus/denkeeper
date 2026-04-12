// Playwright config for setup and approval E2E tests.
// These tests manage their own server instances, so no webServer is needed.
// Global setup/teardown are also skipped since these tests are self-contained.

import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './test/e2e',
  testMatch: ['setup.spec.js', 'approval.spec.js'],
  timeout: 60000,
  retries: 0,
  use: {
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
})
