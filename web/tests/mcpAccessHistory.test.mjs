import assert from 'node:assert/strict'
import test from 'node:test'
import {
  markMCPOriginRecentlyUsed,
  mcpHTTPOrigin,
  mcpWSOrigin,
  normalizeMCPAccessHistory,
  normalizeMCPOrigin,
} from '../src/utils/mcpAccessHistory.ts'

test('normalizes MCP access history defensively', () => {
  assert.deepEqual(
    normalizeMCPAccessHistory({
      origins: [' https://demo.example.com/ ', 'https://demo.example.com', '', 42],
    }),
    { origins: ['https://demo.example.com'] },
  )
  assert.deepEqual(normalizeMCPAccessHistory(null), { origins: [] })
})

test('marks recently used MCP origins first and keeps a bounded history', () => {
  const history = markMCPOriginRecentlyUsed(
    { origins: ['https://a.example.com', 'https://b.example.com'] },
    'https://c.example.com/',
    2,
  )

  assert.deepEqual(history, {
    origins: ['https://c.example.com', 'https://a.example.com'],
  })
})

test('normalizes base origins without changing placeholders', () => {
  assert.equal(normalizeMCPOrigin(' https://demo.example.com/// '), 'https://demo.example.com')
  assert.equal(mcpHTTPOrigin(''), 'http(s)://<your-host>:<port>')
  assert.equal(mcpWSOrigin(''), 'ws(s)://<your-host>:<port>')
})

test('derives HTTP origin from WebSocket origin', () => {
  assert.equal(mcpHTTPOrigin('wss://demo.example.com'), 'https://demo.example.com')
  assert.equal(mcpHTTPOrigin('ws://127.0.0.1:8080'), 'http://127.0.0.1:8080')
  assert.equal(mcpHTTPOrigin('ws(s)://<your-host>:<port>'), 'http(s)://<your-host>:<port>')
})

test('derives WebSocket origin from HTTP origin', () => {
  assert.equal(mcpWSOrigin('https://demo.example.com'), 'wss://demo.example.com')
  assert.equal(mcpWSOrigin('http://127.0.0.1:8080'), 'ws://127.0.0.1:8080')
  assert.equal(mcpWSOrigin('http(s)://<your-host>:<port>'), 'ws(s)://<your-host>:<port>')
  assert.equal(mcpWSOrigin('wss://demo.example.com'), 'wss://demo.example.com')
})
