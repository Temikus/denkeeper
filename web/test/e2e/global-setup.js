// global-setup.js — creates a temporary data directory for E2E tests
// so they never touch ~/.denkeeper or any user-specific state.
// Also starts a mock LLM server for chat tests.

import { mkdtempSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { startMockLLM } from './helpers/mock-llm.js'

const MOCK_LLM_PORT = 19111

export default async function globalSetup() {
  const base = mkdtempSync(join(tmpdir(), 'denkeeper-e2e-'))
  mkdirSync(join(base, 'agents', 'default'), { recursive: true })
  mkdirSync(join(base, 'skills'), { recursive: true })
  mkdirSync(join(base, 'data'), { recursive: true })

  // DENKEEPER_DATA_DIR drives all default paths (db, persona, skills).
  process.env.DENKEEPER_DATA_DIR = base

  // Start mock LLM server for chat tests.
  const server = await startMockLLM(MOCK_LLM_PORT)

  // Stash references for teardown.
  globalThis.__e2eTmpDir = base
  globalThis.__e2eMockLLM = server
}
