import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './test/e2e',
  timeout: 30000,
  retries: 1,
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:8080',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  webServer: process.env.CI ? undefined : {
    command: 'go run -tags mcp_go_client_oauth ../cmd/denkeeper serve --config ./test/e2e/fixtures/test.toml',
    port: 8080,
    reuseExistingServer: true,
    timeout: 60000,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
})
