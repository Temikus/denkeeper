// mock-llm-tools.js — mock LLM server that supports tool calls for approval E2E tests.
// When the user message contains "use echo tool", returns a tool call instead of text.
// On the follow-up request (with tool result), returns a normal text response.

import { createServer } from 'node:http'

const RESPONSE_TEXT = 'Tool result received!'

/**
 * Start a mock LLM server with tool call support.
 * @param {number} port
 * @returns {Promise<import('node:http').Server>}
 */
export function startMockLLMWithTools(port) {
  return new Promise((resolve, reject) => {
    const server = createServer((req, res) => {
      if (req.method === 'POST' && req.url === '/v1/chat/completions') {
        let body = ''
        req.on('data', (chunk) => { body += chunk })
        req.on('end', () => {
          const parsed = JSON.parse(body)
          const lastUserMsg = [...parsed.messages].reverse().find(m => m.role === 'user')
          const hasToolResult = parsed.messages.some(m => m.role === 'tool')

          res.writeHead(200, {
            'Content-Type': 'text/event-stream',
            'Cache-Control': 'no-cache',
            'Connection': 'keep-alive',
          })

          if (lastUserMsg?.content?.includes('use echo tool') && !hasToolResult) {
            // Return a tool call response.
            res.write('data: ' + JSON.stringify({
              id: 'e2e-tc-1',
              model: 'test-model',
              choices: [{
                index: 0,
                delta: {
                  tool_calls: [{
                    index: 0,
                    id: 'call_e2e_echo',
                    type: 'function',
                    function: { name: 'echo', arguments: '' },
                  }],
                },
                finish_reason: null,
              }],
            }) + '\n\n')

            // Second chunk: tool call arguments (streamed incrementally).
            res.write('data: ' + JSON.stringify({
              id: 'e2e-tc-1',
              model: 'test-model',
              choices: [{
                index: 0,
                delta: {
                  tool_calls: [{
                    index: 0,
                    function: { arguments: '{"text":"hello from e2e"}' },
                  }],
                },
                finish_reason: null,
              }],
            }) + '\n\n')

            // Final chunk with finish_reason.
            res.write('data: ' + JSON.stringify({
              id: 'e2e-tc-1',
              model: 'test-model',
              choices: [{
                index: 0,
                delta: {},
                finish_reason: 'tool_calls',
              }],
              usage: { prompt_tokens: 20, completion_tokens: 10, total_tokens: 30 },
            }) + '\n\n')

            res.write('data: [DONE]\n\n')
            res.end()
          } else {
            // Normal text response (used after tool result or for non-tool messages).
            res.write('data: ' + JSON.stringify({
              id: 'e2e-2',
              model: 'test-model',
              choices: [{
                index: 0,
                delta: { content: RESPONSE_TEXT },
                finish_reason: null,
              }],
            }) + '\n\n')

            res.write('data: ' + JSON.stringify({
              id: 'e2e-2',
              model: 'test-model',
              choices: [{
                index: 0,
                delta: {},
                finish_reason: 'stop',
              }],
              usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 },
            }) + '\n\n')

            res.write('data: [DONE]\n\n')
            res.end()
          }
        })
        return
      }

      // Ollama tags endpoint.
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
