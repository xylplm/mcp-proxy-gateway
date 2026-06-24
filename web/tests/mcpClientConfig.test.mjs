import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildMCPClientConfig,
  buildMCPServerConfig,
  getMCPClientPreset,
  MCP_CLIENT_PRESETS,
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

test('client presets cover common clients with stable defaults', () => {
  const keys = MCP_CLIENT_PRESETS.map((item) => item.key)
  assert.deepEqual(keys, ['generic', 'claude', 'cursor', 'vscode', 'cherry-studio'])

  const generic = getMCPClientPreset('generic')
  assert.equal(generic.endpoint, 'http')
  assert.equal(generic.auth, 'bearer')
  assert.equal(generic.configKind, 'mcp-json')

  const cherry = getMCPClientPreset('cherry-studio')
  assert.equal(cherry.configKind, 'server-entry')
})

test('unknown client preset falls back to generic', () => {
  assert.equal(getMCPClientPreset('missing').key, 'generic')
})
