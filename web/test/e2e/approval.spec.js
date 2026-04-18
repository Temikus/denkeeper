// @ts-check
import { test, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { createServer } from 'node:net'
import { startMockLLMWithTools } from './helpers/mock-llm-tools.js'
import { ChatPage } from './helpers/chat.js'

const HERE = import.meta.dirname
const API_TOKEN = 'e2e-test-token-12345678'

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

test.describe('Approval lifecycle', () => {
  /** @type {import('node:child_process').ChildProcess | null} */
  let server = null
  /** @type {import('node:http').Server | null} */
  let mockLLM = null
  let tmpDir = ''
  let BASE = ''

  test.beforeAll(async () => {
    const [serverPort, llmPort] = await Promise.all([freePort(), freePort()])
    BASE = `http://localhost:${serverPort}`

    // Create isolated data directory.
    tmpDir = mkdtempSync(join(tmpdir(), 'denkeeper-e2e-approval-'))
    mkdirSync(join(tmpDir, 'agents', 'default'), { recursive: true })
    mkdirSync(join(tmpDir, 'skills'), { recursive: true })
    mkdirSync(join(tmpDir, 'data'), { recursive: true })

    // Start mock LLM with tool call support.
    mockLLM = await startMockLLMWithTools(llmPort)

    // Write config with supervised tier and dynamic ports.
    const configPath = join(tmpDir, 'test-supervised.toml')
    writeFileSync(configPath, `
[api]
listen = ':${serverPort}'
login_rate_limit = 0

[api.auth]
password_hash = '$2a$13$${'7GUyWo4wSXOVEW5GqDRqOeHggYFzyYvkwhFfl/kPcyCP28.l6vrm.'}'
session_max_age = '24h'
session_secret = '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'

[[api.keys]]
key = '${API_TOKEN}'
name = 'e2e-test'
scopes = ['admin', 'chat', 'sessions:read', 'costs:read', 'skills:read', 'skills:write', 'schedules:read', 'schedules:write', 'approvals:read', 'approvals:write', 'tools:read', 'tools:write', 'kv:read', 'kv:write', 'agents:read', 'agents:write']

[llm]
cost_limit_hard = 100.0
default_model = 'test-model'
default_provider = 'ollama'

[llm.ollama]
base_url = 'http://127.0.0.1:${llmPort}'

[session]
tier = 'supervised'

[web]
enabled = false
`)

    const mockToolPath = join(HERE, 'helpers', 'mock-mcp-tool.js')
    const repoRoot = join(HERE, '..', '..', '..')

    // In CI the pre-built binary is downloaded to the repo root; locally use go run.
    const isCI = !!process.env.CI
    const binaryPath = process.env.DENKEEPER_BINARY || join(repoRoot, 'denkeeper-linux-amd64')
    const spawnCmd = isCI ? binaryPath : 'go'
    const spawnArgs = isCI
      ? ['serve', '--config', configPath]
      : ['run', './cmd/denkeeper', 'serve', '--config', configPath]
    const spawnCwd = isCI ? undefined : repoRoot

    // Start denkeeper with supervised config.
    let output = ''
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error(`server start timed out on :${serverPort}. Output:\n${output}`))
      }, 60000)

      server = spawn(
        spawnCmd,
        spawnArgs,
        {
          cwd: spawnCwd,
          env: { ...process.env, DENKEEPER_DATA_DIR: tmpDir },
          stdio: ['ignore', 'pipe', 'pipe'],
        },
      )

      server.stdout.on('data', (chunk) => { output += chunk.toString() })
      server.stderr.on('data', (chunk) => { output += chunk.toString() })
      server.on('error', (err) => { clearTimeout(timeout); reject(err) })
      server.on('exit', (code) => {
        clearTimeout(timeout)
        reject(new Error(`server exited with code ${code}. Output:\n${output}`))
      })

      // Poll health endpoint.
      const poll = async () => {
        for (let i = 0; i < 120; i++) {
          try {
            const res = await fetch(`${BASE}/api/v1/health`)
            if (res.ok) { clearTimeout(timeout); return resolve(undefined) }
          } catch { /* not ready */ }
          await new Promise((r) => setTimeout(r, 500))
        }
        clearTimeout(timeout)
        reject(new Error(`health endpoint never became ready on :${serverPort}. Output:\n${output}`))
      }
      poll()
    })

    // Add the mock echo tool via API.
    const addToolRes = await fetch(`${BASE}/api/v1/tools`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${API_TOKEN}`,
      },
      body: JSON.stringify({
        name: 'echo-tool',
        command: 'node',
        args: [mockToolPath],
      }),
    })
    if (!addToolRes.ok) {
      const errBody = await addToolRes.text()
      throw new Error(`Failed to add echo tool: ${addToolRes.status} ${errBody}`)
    }

    // Verify the tool was registered and has the echo tool.
    const toolsRes = await fetch(`${BASE}/api/v1/tools`, {
      headers: { 'Authorization': `Bearer ${API_TOKEN}` },
    })
    const toolsBody = await toolsRes.json()
    const echoServer = toolsBody.tools?.find(t => t.name === 'echo-tool')
    if (!echoServer || !echoServer.tool_names?.includes('echo')) {
      throw new Error(`echo tool not registered. Tools: ${JSON.stringify(toolsBody)}`)
    }
  })

  test.afterAll(async () => {
    if (server && !server.killed) {
      server.kill('SIGTERM')
      await new Promise((resolve) => {
        server.on('close', resolve)
        setTimeout(() => { try { server.kill('SIGKILL') } catch {}; resolve(undefined) }, 5000)
      })
    }
    if (mockLLM) {
      await new Promise((resolve) => mockLLM.close(resolve))
    }
    if (tmpDir) {
      try { rmSync(tmpDir, { recursive: true, force: true }) } catch { /* best effort */ }
    }
  })

  test('tool is registered and server is supervised', async () => {
    // Verify the echo tool is available.
    const toolsRes = await fetch(`${BASE}/api/v1/tools`, {
      headers: { 'Authorization': `Bearer ${API_TOKEN}` },
    })
    expect(toolsRes.ok).toBe(true)
    const toolsBody = await toolsRes.json()
    const echoServer = toolsBody.tools?.find(t => t.name === 'echo-tool')
    expect(echoServer).toBeTruthy()
    expect(echoServer.tool_names).toContain('echo')
    expect(echoServer.status).toBe('connected')
  })

  test('chat triggers tool call and creates pending approval', async () => {
    // Send a chat message that triggers a tool call. The SSE stream stays open
    // waiting for approval, so we read it with AbortController and a timeout.
    const controller = new AbortController()
    const chatPromise = fetch(`${BASE}/api/v1/chat`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${API_TOKEN}`,
        'Accept': 'text/event-stream',
      },
      body: JSON.stringify({
        message: 'Please use echo tool',
        agent: 'default',
      }),
      signal: controller.signal,
    })

    const chatRes = await chatPromise
    expect(chatRes.ok).toBe(true)

    // Read SSE events from the stream until we see a tool_approval event.
    const reader = chatRes.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let foundApproval = false

    const readTimeout = setTimeout(() => controller.abort(), 15000)
    try {
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        if (buffer.includes('tool_approval')) {
          foundApproval = true
          break
        }
      }
    } catch (e) {
      if (e.name !== 'AbortError') throw e
    } finally {
      clearTimeout(readTimeout)
      controller.abort() // stop reading the stream
    }

    expect(foundApproval).toBe(true)

    // Check that there is a pending approval in the system.
    const approvalsRes = await fetch(`${BASE}/api/v1/approvals?status=pending`, {
      headers: { 'Authorization': `Bearer ${API_TOKEN}` },
    })
    expect(approvalsRes.ok).toBe(true)
    const approvals = await approvalsRes.json()
    expect(approvals.length).toBeGreaterThan(0)
    expect(approvals[0].summary).toContain('echo')
  })

  test('approving a pending approval resolves it', async () => {
    // Get pending approvals.
    const approvalsRes = await fetch(`${BASE}/api/v1/approvals?status=pending`, {
      headers: { 'Authorization': `Bearer ${API_TOKEN}` },
    })
    const approvals = await approvalsRes.json()
    test.skip(approvals.length === 0, 'no pending approvals from previous test')

    const approvalId = approvals[0].id

    // Approve it.
    const approveRes = await fetch(`${BASE}/api/v1/approvals/${approvalId}/approve`, {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${API_TOKEN}` },
    })
    expect(approveRes.ok).toBe(true)

    // Verify it is no longer pending.
    const checkRes = await fetch(`${BASE}/api/v1/approvals/${approvalId}`, {
      headers: { 'Authorization': `Bearer ${API_TOKEN}` },
    })
    expect(checkRes.ok).toBe(true)
    const updated = await checkRes.json()
    expect(updated.status).toBe('approved')
  })

  test('approve tool call via browser UI', async ({ page }, testInfo) => {
    testInfo.setTimeout(60000)
    // Log in.
    await page.goto(BASE)
    await page.fill('input[type="password"]', 'test')
    await page.click('button[type="submit"]')
    await page.locator('nav').waitFor({ state: 'visible', timeout: 10000 })

    // Navigate to Chat and send a message that triggers a tool call.
    const chat = new ChatPage(page)
    await page.goto(`${BASE}/#/chat`)
    await page.locator('[data-testid="chat-input"]').waitFor({ state: 'visible', timeout: 10000 })
    await chat.sendMessage('Please use echo tool')

    // Wait for the pending approval card to appear in the chat bubble.
    const pendingCard = page.locator('.bubble .approval-card.pending')
    await pendingCard.first().waitFor({ state: 'visible', timeout: 20000 })

    // Verify the tool name is shown.
    await expect(pendingCard.first().locator('.tool-name')).toContainText('echo')

    // Click Approve.
    await pendingCard.first().locator('.btn-appr.btn-ok').click()

    // Wait for the approval badge to show "approved".
    await expect(
      page.locator('.bubble .approval-card .approval-badge').filter({ hasText: 'approved' }),
    ).toBeVisible({ timeout: 10000 })

    // Wait for the tool call to complete.
    await expect(
      page.locator('.bubble .tool-call.done'),
    ).toBeVisible({ timeout: 10000 })

    // Wait for the final assistant response with text from the mock LLM.
    await expect(
      page.locator('.bubble.agent .text').filter({ hasText: 'Tool result received!' }),
    ).toBeVisible({ timeout: 15000 })
  })

  test('deny tool call via browser UI', async ({ page }, testInfo) => {
    testInfo.setTimeout(60000)
    // Log in.
    await page.goto(BASE)
    await page.fill('input[type="password"]', 'test')
    await page.click('button[type="submit"]')
    await page.locator('nav').waitFor({ state: 'visible', timeout: 10000 })

    // Navigate to Chat and send a message that triggers a tool call.
    const chat = new ChatPage(page)
    await page.goto(`${BASE}/#/chat`)
    await page.locator('[data-testid="chat-input"]').waitFor({ state: 'visible', timeout: 10000 })
    await chat.sendMessage('Please use echo tool')

    // Wait for the pending approval card to appear.
    const pendingCard = page.locator('.bubble .approval-card.pending')
    await pendingCard.first().waitFor({ state: 'visible', timeout: 20000 })

    // Click Deny.
    await pendingCard.first().locator('.btn-appr.btn-bad').click()

    // Wait for the approval badge to show "denied".
    await expect(
      page.locator('.bubble .approval-card .approval-badge').filter({ hasText: 'denied' }),
    ).toBeVisible({ timeout: 10000 })

    // The LLM receives "Tool call was denied" and produces a follow-up response.
    // The mock LLM returns "Tool result received!" for any non-tool-call message.
    await expect(
      page.locator('.bubble.agent:not(.streaming)').last(),
    ).toBeVisible({ timeout: 15000 })
  })
})
