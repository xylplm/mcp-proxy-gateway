import assert from 'node:assert/strict'
import test from 'node:test'
import {
  formatAllowlist,
  stdioPolicyLabel,
  summarizeToolHealth,
  toolStatusLabel,
  toolStatusTone,
} from '../src/utils/runtimeSummary.ts'

test('tool status labels and tones', () => {
  assert.equal(toolStatusLabel({ name: 'node', available: true }), '可用')
  assert.equal(toolStatusTone({ name: 'node', available: true }), 'success')
  assert.equal(toolStatusLabel({ name: 'uvx', available: false }), '未检测到')
  assert.equal(toolStatusTone({ name: 'uvx', available: false }), 'warning')
})

test('summarizeToolHealth covers all/missing/empty', () => {
  assert.equal(
    summarizeToolHealth({
      availableCount: 3,
      missingCount: 0,
      tools: [{ name: 'a', available: true }],
    }),
    '已检测到 3 个常用工具',
  )
  assert.equal(
    summarizeToolHealth({
      availableCount: 1,
      missingCount: 2,
      tools: [],
    }),
    '可用 1 · 缺失 2',
  )
  assert.equal(summarizeToolHealth({ availableCount: 0, missingCount: 0, tools: [] }), '暂无探测项')
})

test('policy and allowlist formatting', () => {
  assert.equal(stdioPolicyLabel(true), '本地 stdio 已启用')
  assert.equal(stdioPolicyLabel(false), '本地 stdio 已禁用')
  assert.equal(formatAllowlist(['node', 'npx']), 'node、npx')
  assert.equal(formatAllowlist([]), '未配置（使用服务端默认）')
  assert.equal(formatAllowlist(null), '未配置（使用服务端默认）')
})
