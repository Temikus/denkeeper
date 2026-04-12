// global-teardown.js — shuts down the mock LLM server and cleans up temp directories.

import { rmSync } from 'node:fs'

export default async function globalTeardown() {
  // Shut down mock LLM server.
  if (globalThis.__e2eMockLLM) {
    await new Promise((resolve) => globalThis.__e2eMockLLM.close(resolve))
  }

  // Clean up temp data directory.
  if (globalThis.__e2eTmpDir) {
    try {
      rmSync(globalThis.__e2eTmpDir, { recursive: true, force: true })
    } catch {
      // Best-effort cleanup.
    }
  }
}
