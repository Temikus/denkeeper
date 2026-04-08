// global-setup.js — creates a temporary data directory for E2E tests
// so they never touch ~/.denkeeper or any user-specific state.

import { mkdtempSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'

export default function globalSetup() {
  const base = mkdtempSync(join(tmpdir(), 'denkeeper-e2e-'))
  mkdirSync(join(base, 'agents', 'default'), { recursive: true })
  mkdirSync(join(base, 'skills'), { recursive: true })
  mkdirSync(join(base, 'data'), { recursive: true })

  // DENKEEPER_DATA_DIR drives all default paths (db, persona, skills).
  process.env.DENKEEPER_DATA_DIR = base
}
