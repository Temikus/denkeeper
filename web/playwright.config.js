import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './test/e2e',
  testIgnore: ['setup.spec.js', 'approval.spec.js'],
  globalSetup: './test/e2e/global-setup.js',
  globalTeardown: './test/e2e/global-teardown.js',
  timeout: 30000,
  retries: process.env.CI ? 2 : 0,
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:8080',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  webServer: process.env.CI ? undefined : {
    command: 'go run ../cmd/denkeeper serve --config ./test/e2e/fixtures/test.toml',
    port: 8080,
    reuseExistingServer: true,
    timeout: 60000,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
})
