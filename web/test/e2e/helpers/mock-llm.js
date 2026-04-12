// mock-llm.js — lightweight HTTP server that mimics the Ollama /api/chat endpoint.
// Returns a deterministic streamed response for E2E tests.

import { createServer } from 'node:http'

const RESPONSE_TEXT = 'Hello from E2E mock!'

/**
 * Start a mock Ollama server on the given port.
 * @param {number} port
 * @returns {Promise<import('node:http').Server>}
 */
export function startMockLLM(port) {
  return new Promise((resolve, reject) => {
    const server = createServer((req, res) => {
      // Ollama chat endpoint: POST /api/chat
      if (req.method === 'POST' && req.url === '/api/chat') {
        // Drain request body before responding.
        let body = ''
        req.on('data', (chunk) => { body += chunk })
        req.on('end', () => {
          res.writeHead(200, { 'Content-Type': 'application/x-ndjson' })

          // Ollama streams newline-delimited JSON objects.
          // First chunk: partial content.
          res.write(JSON.stringify({
            model: 'test-model',
            created_at: new Date().toISOString(),
            message: { role: 'assistant', content: RESPONSE_TEXT },
            done: false,
          }) + '\n')

          // Final chunk: done with usage stats.
          res.write(JSON.stringify({
            model: 'test-model',
            created_at: new Date().toISOString(),
            message: { role: 'assistant', content: '' },
            done: true,
            total_duration: 100000000,
            prompt_eval_count: 10,
            eval_count: 5,
          }) + '\n')

          res.end()
        })
        return
      }

      // Ollama tags endpoint: GET /api/tags (used for model listing).
      if (req.method === 'GET' && req.url === '/api/tags') {
        res.writeHead(200, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({
          models: [{ name: 'test-model', size: 0, digest: 'e2e', modified_at: new Date().toISOString() }],
        }))
        return
      }

      res.writeHead(404)
      res.end('Not found')
    })

    server.listen(port, '127.0.0.1', () => resolve(server))
    server.on('error', reject)
  })
}
