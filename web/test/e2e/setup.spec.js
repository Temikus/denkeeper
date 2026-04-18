// @ts-check
import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { createServer } from 'node:net'

const HERE = import.meta.dirname

/** Find a free port by briefly binding to port 0. */
function freePort() {
  return new Promise((resolve, reject) => {
    const srv = createServer()
    srv.listen(0, '127.0.0.1', () => {
      const port = srv.address().port
      srv.close(() => resolve(port))
    })
    srv.on('error', reject)
  })
}

test.describe('First-run setup wizard', () => {
  /** @type {import('node:child_process').ChildProcess | null} */
  let server = null
  let setupPIN = ''
  let tmpDir = ''
  let BASE = ''

  test.beforeAll(async () => {
    const port = await freePort()
    BASE = `http://localhost:${port}`

    // Create an isolated data directory for the setup server.
    tmpDir = mkdtempSync(join(tmpdir(), 'denkeeper-e2e-setup-'))
    mkdirSync(join(tmpDir, 'agents', 'default'), { recursive: true })
    mkdirSync(join(tmpDir, 'skills'), { recursive: true })
    mkdirSync(join(tmpDir, 'data'), { recursive: true })

    // Write config dynamically with the chosen port — no auth configured.
    const configPath = join(tmpDir, 'test-setup.toml')
    writeFileSync(configPath, `
[api]
listen = ':${port}'
login_rate_limit = 0

[llm]
cost_limit_hard = 100.0
default_model = 'test-model'
default_provider = 'ollama'

[llm.ollama]
base_url = 'http://127.0.0.1:19111'

[session]
tier = 'autonomous'

[web]
enabled = false
`)

    const repoRoot = join(HERE, '..', '..', '..')

    // Start denkeeper and capture the setup PIN from stdout.
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error(`server start timed out on :${port}. PIN: ${setupPIN || 'none'}. Output:\n${output}`))
      }, 60000)

      server = spawn(
        'go',
        ['run', '-tags', 'mcp_go_client_oauth', './cmd/denkeeper', 'serve', '--config', configPath],
        {
          cwd: repoRoot,
          env: { ...process.env, DENKEEPER_DATA_DIR: tmpDir },
          stdio: ['ignore', 'pipe', 'pipe'],
        },
      )

      let output = ''
      const onData = (chunk) => {
        output += chunk.toString()
        const match = output.match(/FIRST-RUN SETUP PIN.*?pin=(\d{6})/)
        if (match) setupPIN = match[1]
      }

      server.stdout.on('data', onData)
      server.stderr.on('data', onData)
      server.on('error', (err) => { clearTimeout(timeout); reject(err) })
      server.on('exit', (code) => {
        clearTimeout(timeout)
        reject(new Error(`server exited with code ${code} before ready. Output:\n${output}`))
      })

      // Poll the health endpoint until the server is ready.
      const poll = async () => {
        for (let i = 0; i < 120; i++) {
          try {
            const res = await fetch(`${BASE}/api/v1/health`)
            if (res.ok) { clearTimeout(timeout); return resolve(undefined) }
          } catch { /* server not up yet */ }
          await new Promise((r) => setTimeout(r, 500))
        }
        clearTimeout(timeout)
        reject(new Error(`health endpoint never became ready on :${port}. Output:\n${output}`))
      }
      poll()
    })
  })

  test.afterAll(async () => {
    if (server && !server.killed) {
      server.kill('SIGTERM')
      await new Promise((resolve) => {
        server.on('close', resolve)
        setTimeout(() => { try { server.kill('SIGKILL') } catch {}; resolve(undefined) }, 5000)
      })
    }
    if (tmpDir) {
      try { rmSync(tmpDir, { recursive: true, force: true }) } catch { /* best effort */ }
    }
  })

  test('setup status reports setup_required', async () => {
    const res = await fetch(`${BASE}/api/v1/setup`)
    expect(res.ok).toBe(true)
    const body = await res.json()
    expect(body.setup_required).toBe(true)
    expect(body.account_setup_available).toBe(true)
  })

  test('shows setup wizard on first visit', async ({ page }) => {
    await page.goto(BASE)
    await expect(page.locator('h1:has-text("Welcome to Denkeeper")')).toBeVisible({ timeout: 10000 })
    await expect(page.locator('button.tab:has-text("Create Account")')).toBeVisible()
    await expect(page.locator('button.tab:has-text("Create API Key")')).toBeVisible()
  })

  test('can create account with PIN and password', async ({ page }) => {
    test.skip(!setupPIN, 'PIN not captured from server logs')

    await page.goto(BASE)
    await expect(page.locator('h1:has-text("Welcome to Denkeeper")')).toBeVisible({ timeout: 10000 })

    await page.locator('input[placeholder="6-digit PIN from server logs"]').fill(setupPIN)
    await page.locator('input[placeholder="Choose a password (min. 8 characters)"]').fill('strongpassword123')
    await page.locator('input[placeholder="Confirm your password"]').fill('strongpassword123')

    await page.getByRole('button', { name: 'Create account', exact: true }).click()

    // After successful account setup, user should be logged in (nav bar visible).
    await expect(page.locator('nav')).toBeVisible({ timeout: 10000 })

    // Verify redirect to Overview page with live health data.
    await expect(page.locator('.card .label')).first().toBeVisible({ timeout: 10000 })

    // Health status should show "ok".
    const statusValue = page.locator('.card').filter({ has: page.locator('.label', { hasText: 'Status' }) }).locator('.value')
    await expect(statusValue).toHaveText('ok', { timeout: 10000 })

    // Agents count should be at least 1 (the "default" agent).
    const agentsValue = page.locator('.card').filter({ has: page.locator('.label', { hasText: 'Agents' }) }).locator('.value')
    await expect(agentsValue).toBeVisible()
    const agentCount = await agentsValue.textContent()
    expect(Number(agentCount)).toBeGreaterThanOrEqual(1)

    // Agent card section should render with the "default" agent.
    await expect(page.locator('.agent-card .agent-name').filter({ hasText: 'default' })).toBeVisible({ timeout: 5000 })
  })

  test('can create API key via setup wizard', async () => {
    // Since the account test above may have completed setup, check status first.
    const statusRes = await fetch(`${BASE}/api/v1/setup`)
    const status = await statusRes.json()
    test.skip(!status.setup_required, 'setup already completed by previous test')

    // Use the API directly since browser tests may not work in all environments.
    const createRes = await fetch(`${BASE}/api/v1/setup`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: 'e2e-admin', scopes: ['admin'] }),
    })
    expect(createRes.status).toBe(201)
    const key = await createRes.json()
    expect(key.key).toBeTruthy()
    expect(key.key.startsWith('dk_')).toBe(true)
    expect(key.name).toBe('e2e-admin')
  })
})
