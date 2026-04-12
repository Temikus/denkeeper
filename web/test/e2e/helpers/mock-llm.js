// mock-llm.js — lightweight HTTP server that mimics Ollama's OpenAI-compatible
// /v1/chat/completions endpoint. Returns a deterministic SSE-streamed response.

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
      // OpenAI-compatible chat completions (used by Ollama provider).
      if (req.method === 'POST' && req.url === '/v1/chat/completions') {
        let body = ''
        req.on('data', (chunk) => { body += chunk })
        req.on('end', () => {
          res.writeHead(200, {
            'Content-Type': 'text/event-stream',
            'Cache-Control': 'no-cache',
            'Connection': 'keep-alive',
          })

          // SSE chunk with content.
          res.write('data: ' + JSON.stringify({
            id: 'e2e-1',
            model: 'test-model',
            choices: [{
              index: 0,
              delta: { content: RESPONSE_TEXT },
              finish_reason: null,
            }],
          }) + '\n\n')

          // Final SSE chunk with finish_reason and usage.
          res.write('data: ' + JSON.stringify({
            id: 'e2e-1',
            model: 'test-model',
            choices: [{
              index: 0,
              delta: {},
              finish_reason: 'stop',
            }],
            usage: {
              prompt_tokens: 10,
              completion_tokens: 5,
              total_tokens: 15,
            },
          }) + '\n\n')

          // SSE done sentinel.
          res.write('data: [DONE]\n\n')
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
