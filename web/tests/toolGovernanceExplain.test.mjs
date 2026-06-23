import assert from 'node:assert/strict'
import test from 'node:test'
import { explainToolGovernance } from '../src/utils/toolGovernanceExplain.ts'

function detail(overrides = {}) {
  return {
    tool: {
      originalName: 'read_file',
      name: 'read_file',
      description: '读取文件',
      inputSchema: { type: 'object' },
      upstreamId: 'up-1',
      order: 0,
      sourceCount: 1,
      schemaConflict: false,
    },
    sources: [
      {
        upstreamId: 'up-1',
        upstreamName: '文件服务',
        originalName: 'read_file',
        description: '读取文件',
        inputSchema: { type: 'object' },
        compatible: true,
        schemaConflict: false,
        rateLimits: { enabled: false },
      },
    ],
    ...overrides,
  }
}

test('explains a simple single-source tool as normally callable', () => {
  const summary = explainToolGovernance(detail())

  assert.equal(summary.title, '当前可正常调用')
  assert.match(summary.description, /1 个来源可参与调用路由/)
  assert.equal(summary.items.find((item) => item.key === 'source')?.title, '单一来源')
  assert.equal(summary.items.find((item) => item.key === 'routing')?.tone, 'success')
})

test('explains alias and multiple routed sources', () => {
  const summary = explainToolGovernance(
    detail({
      tool: {
        originalName: 'read_file',
        name: 'fs_read',
        description: '读取文件',
        inputSchema: { type: 'object' },
        upstreamId: 'up-1',
        order: 0,
        sourceCount: 2,
        schemaConflict: false,
      },
      sources: [
        {
          upstreamId: 'up-1',
          upstreamName: '文件服务 A',
          originalName: 'read_file',
          description: 'read',
          inputSchema: { type: 'object' },
          compatible: true,
          schemaConflict: false,
          rateLimits: { enabled: false },
        },
        {
          upstreamId: 'up-2',
          upstreamName: '文件服务 B',
          originalName: 'read_file',
          description: 'read',
          inputSchema: { type: 'object' },
          compatible: true,
          schemaConflict: false,
          rateLimits: { enabled: true, perMinute: 60 },
        },
      ],
    }),
  )

  assert.equal(summary.title, '多来源工具，当前可正常调用')
  assert.match(summary.items.find((item) => item.key === 'source')?.description ?? '', /2 个上游来源/)
  assert.equal(summary.items.find((item) => item.key === 'alias')?.title, '名称 / 描述已治理')
  assert.equal(summary.items.find((item) => item.key === 'rate-limit')?.title, '上游限流参与路由')
})

test('keeps schema conflicts visible without marking all sources unusable', () => {
  const summary = explainToolGovernance(
    detail({
      tool: {
        originalName: 'search',
        name: 'search',
        description: '搜索',
        inputSchema: { type: 'object', properties: { q: { type: 'string' } } },
        upstreamId: 'up-1',
        order: 0,
        sourceCount: 2,
        schemaConflict: true,
      },
      sources: [
        {
          upstreamId: 'up-1',
          upstreamName: '搜索 A',
          originalName: 'search',
          description: '搜索',
          inputSchema: { type: 'object', properties: { q: { type: 'string' } } },
          compatible: true,
          schemaConflict: true,
          rateLimits: { enabled: false },
        },
        {
          upstreamId: 'up-2',
          upstreamName: '搜索 B',
          originalName: 'search',
          description: '搜索',
          inputSchema: { type: 'object', properties: { query: { type: 'string' } } },
          compatible: false,
          schemaConflict: true,
          rateLimits: { enabled: false },
        },
      ],
    }),
  )

  assert.equal(summary.title, '可见，但存在治理提醒')
  assert.match(summary.description, /1 个来源可参与调用/)
  assert.equal(summary.items.find((item) => item.key === 'schema')?.tone, 'warning')
  assert.match(summary.items.find((item) => item.key === 'routing')?.description ?? '', /1 个来源可参与路由/)
})
