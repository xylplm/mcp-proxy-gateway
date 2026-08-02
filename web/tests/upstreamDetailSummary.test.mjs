import assert from 'node:assert/strict'
import test from 'node:test'
import { buildUpstreamDetailSummary } from '../src/utils/upstreamDetailSummary.ts'

function upstream(overrides = {}) {
  return {
    id: 'up-1',
    config: {
      name: '文件服务',
      tags: ['local'],
      transport: 'streamable-http',
      connParams: { url: 'http://127.0.0.1:3000/mcp', headers: { 'X-Team': 'dev' } },
      credential: 'secret',
      enabled: true,
      sortOrder: 0,
      autoSync: true,
      rateLimits: { enabled: false },
    },
    state: 'available',
    lastError: '',
    failureCount: 0,
    nextRetryAt: null,
    createdAt: '2026-06-24T08:00:00Z',
    updatedAt: '2026-06-24T09:00:00Z',
    ...overrides,
  }
}

test('summarizes an available HTTP upstream', () => {
  const summary = buildUpstreamDetailSummary(upstream(), { count: 12, synced: true })

  assert.equal(summary.endpointLabel, '连接地址')
  assert.equal(summary.endpointValue, 'http://127.0.0.1:3000/mcp')
  assert.equal(summary.toolLabel, '12 个工具')
  assert.match(summary.healthDescription, /连接可用/)
  assert.equal(summary.connectionItems.find((item) => item.label === '请求头')?.value, '1 个请求头')
  assert.equal(summary.connectionItems.find((item) => item.label === '访问凭证')?.value, '已配置')
})

test('summarizes stdio security profile label', () => {
  const summary = buildUpstreamDetailSummary(
    upstream({
      config: {
        name: '本地',
        tags: [],
        transport: 'stdio',
        connParams: {
          command: 'node',
          securityProfile: { mode: 'unrestricted', note: 'lab' },
        },
        credential: '',
        enabled: true,
        sortOrder: 0,
        autoSync: true,
        rateLimits: { enabled: false },
      },
    }),
  )
  assert.equal(
    summary.connectionItems.find((item) => item.label === '本地安全档位')?.value.includes('完全放行'),
    true,
  )
})

test('summarizes stdio command and unsynced cache', () => {
  const summary = buildUpstreamDetailSummary(
    upstream({
      config: {
        name: '本地脚本',
        tags: [],
        transport: 'stdio',
        connParams: { command: 'node', args: ['server.js'], cwd: 'D:/work' },
        credential: '',
        enabled: true,
        sortOrder: 2,
        autoSync: false,
        rateLimits: { enabled: true, perMinute: 30 },
      },
      state: 'unavailable',
      lastError: 'connect failed',
    }),
    { count: 0, synced: false },
  )

  assert.equal(summary.endpointLabel, '启动命令')
  assert.equal(summary.endpointValue, 'node')
  assert.equal(summary.toolLabel, '尚未同步')
  assert.match(summary.syncDescription, /手动刷新工具列表/)
  assert.match(summary.healthDescription, /最近错误：connect failed/)
  assert.equal(summary.connectionItems.find((item) => item.label === '限流额度')?.value, '每分钟 30')
  assert.equal(summary.connectionItems.find((item) => item.label === '命令参数')?.value, 'server.js')
})

test('describes sustained automatic recovery with retry metadata', () => {
  const summary = buildUpstreamDetailSummary(
    upstream({
      state: 'suspended',
      lastError: 'connection refused',
      failureCount: 12,
      nextRetryAt: '2026-08-01T16:00:00Z',
    }),
  )

  assert.match(summary.healthDescription, /低频自动恢复/)
  assert.match(summary.healthDescription, /最近错误：connection refused/)
  assert.equal(summary.runtimeItems.find((item) => item.label === '连续失败')?.value, '12 次')
  assert.notEqual(summary.runtimeItems.find((item) => item.label === '下次探测')?.value, '')
})
