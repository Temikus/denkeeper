#!/usr/bin/env node
// mock-mcp-tool.js — minimal MCP stdio server that exposes a single "echo" tool.
// Uses newline-delimited JSON (NDJSON), matching the MCP Go SDK's stdio transport.

import { createInterface } from 'node:readline'

function send(msg) {
  process.stdout.write(JSON.stringify(msg) + '\n')
}

const rl = createInterface({ input: process.stdin })

rl.on('line', (line) => {
  if (!line.trim()) return
  let msg
  try {
    msg = JSON.parse(line)
  } catch (e) {
    process.stderr.write(`mock-mcp-tool: parse error: ${e.message}\n`)
    return
  }

  switch (msg.method) {
    case 'initialize':
      send({
        jsonrpc: '2.0',
        id: msg.id,
        result: {
          protocolVersion: '2024-11-05',
          capabilities: { tools: {} },
          serverInfo: { name: 'mock-echo', version: '1.0.0' },
        },
      })
      break

    case 'notifications/initialized':
      // No response needed for notifications.
      break

    case 'tools/list':
      send({
        jsonrpc: '2.0',
        id: msg.id,
        result: {
          tools: [{
            name: 'echo',
            description: 'Echoes the input text',
            inputSchema: {
              type: 'object',
              properties: { text: { type: 'string', description: 'Text to echo' } },
              required: ['text'],
            },
          }],
        },
      })
      break

    case 'tools/call': {
      const text = msg.params?.arguments?.text || 'no input'
      send({
        jsonrpc: '2.0',
        id: msg.id,
        result: {
          content: [{ type: 'text', text: `Echo: ${text}` }],
        },
      })
      break
    }

    default:
      if (msg.id != null) {
        send({
          jsonrpc: '2.0',
          id: msg.id,
          error: { code: -32601, message: `Method not found: ${msg.method}` },
        })
      }
  }
})
