import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildMCPClientConfig,
  buildMCPServerConfig,
  normalizeMCPServerName,
} from '../src/utils/mcpClientConfig.ts'

test('normalizes empty server names', () => {
  assert.equal(normalizeMCPServerName(''), 'mcp-proxy-gateway')
  assert.equal(normalizeMCPServerName('  custom  '), 'custom')
})

test('builds full mcpServers JSON with headers', () => {
  const raw = buildMCPClientConfig(
    {
      serverName: 'gateway',
      clientType: 'streamable-http',
      url: 'https://example.com/mcp/http',
      headers: { Authorization: 'Bearer <API Key>' },
    },
    'mcp-json',
  )
  const config = JSON.parse(raw)

  assert.equal(config.mcpServers.gateway.type, 'streamable-http')
  assert.equal(config.mcpServers.gateway.url, 'https://example.com/mcp/http')
  assert.equal(config.mcpServers.gateway.headers.Authorization, 'Bearer <API Key>')
})

test('builds server entry JSON and removes empty headers', () => {
  const raw = buildMCPClientConfig(
    {
      serverName: 'ignored',
      clientType: 'websocket',
      url: 'wss://example.com/mcp/ws?api_key=<API Key>',
      headers: { Authorization: '   ' },
    },
    'server-entry',
  )
  const entry = JSON.parse(raw)

  assert.equal(entry.type, 'websocket')
  assert.equal(entry.url, 'wss://example.com/mcp/ws?api_key=<API Key>')
  assert.equal('headers' in entry, false)
})

test('buildMCPServerConfig keeps the minimal shape stable', () => {
  assert.deepEqual(
    buildMCPServerConfig({
      serverName: 'gateway',
      clientType: 'sse',
      url: 'https://example.com/mcp/sse',
    }),
    { type: 'sse', url: 'https://example.com/mcp/sse' },
  )
})
