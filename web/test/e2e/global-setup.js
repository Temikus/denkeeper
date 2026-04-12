// global-setup.js — creates a temporary data directory for E2E tests
// so they never touch ~/.denkeeper or any user-specific state.
// Also starts a mock LLM server for chat tests.
//
// In CI, the data directory is created by the workflow step and
// DENKEEPER_DATA_DIR is already set. We skip creating a second one.

import { mkdtempSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { startMockLLM } from './helpers/mock-llm.js'

const MOCK_LLM_PORT = 19111

export default async function globalSetup() {
  // Only create a temp data dir when running locally (not CI).
  // In CI, the workflow step creates the dir and sets DENKEEPER_DATA_DIR.
  if (!process.env.CI) {
    const base = mkdtempSync(join(tmpdir(), 'denkeeper-e2e-'))
    mkdirSync(join(base, 'agents', 'default'), { recursive: true })
    mkdirSync(join(base, 'skills'), { recursive: true })
    mkdirSync(join(base, 'data'), { recursive: true })
    process.env.DENKEEPER_DATA_DIR = base
    globalThis.__e2eTmpDir = base
  }

  // Start mock LLM server for chat tests.
  const server = await startMockLLM(MOCK_LLM_PORT)
  globalThis.__e2eMockLLM = server
}
