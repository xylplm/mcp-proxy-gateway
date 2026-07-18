import assert from 'node:assert/strict'
import test from 'node:test'
import {
  formatAllowlist,
  packageStatusLabel,
  packageStatusTone,
  runtimeBinDir,
  runtimeGuideSteps,
  sandboxHardeningLabel,
  shouldShowRuntimeGuide,
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

test('runtime volume guide helpers', () => {
  assert.equal(runtimeBinDir({ runtimeDir: '/data/runtime', pathPrefixes: ['/data/runtime/bin'] }), '/data/runtime/bin')
  assert.equal(runtimeBinDir({ runtimeDir: '/data/runtime', pathPrefixes: [] }), '/data/runtime/bin')
  assert.equal(runtimeBinDir({ runtimeDir: '', pathPrefixes: [] }), '')
  assert.equal(shouldShowRuntimeGuide({ missingCount: 2, runtimeDir: '/data/runtime' }), true)
  assert.equal(shouldShowRuntimeGuide({ missingCount: 0, runtimeDir: '/data/runtime' }), false)
  assert.equal(shouldShowRuntimeGuide({ missingCount: 1, runtimeDir: '' }), false)
  const steps = runtimeGuideSteps({ runtimeDir: '/data/runtime', pathPrefixes: [] })
  assert.equal(steps.length, 3)
  assert.match(steps[0], /预置安装|bin/)
})

test('package and sandbox labels', () => {
  assert.equal(packageStatusLabel({ installed: true, supported: true }), '已安装')
  assert.equal(packageStatusLabel({ installed: false, supported: false }), '当前平台不可用')
  assert.equal(packageStatusTone({ installed: true, supported: true }), 'success')
  assert.equal(packageStatusTone({ installed: false, supported: false }), 'muted')
  assert.equal(
    sandboxHardeningLabel({
      processHardening: true,
      sandbox: { processHardeningSupported: true, platform: 'linux', description: 'x' },
    }),
    'Linux 进程加固已启用',
  )
  assert.equal(sandboxHardeningLabel({ processHardening: false, sandbox: undefined }), '进程加固已关闭')
})
