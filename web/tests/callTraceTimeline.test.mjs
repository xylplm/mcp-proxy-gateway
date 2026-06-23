import assert from 'node:assert/strict'
import test from 'node:test'
import { buildCallTraceTimeline } from '../src/utils/callTraceTimeline.ts'

function baseRecord(overrides = {}) {
  return {
    ID: 7,
    UpstreamID: 'up-1',
    UpstreamName: '文件服务',
    OriginalName: 'read_file',
    ExposedName: 'fs_read',
    APIKeyID: 'key-1',
    APIKeyName: '开发 Key',
    CalledAt: '2026-06-24T08:00:00.000Z',
    LatencyMS: 42,
    Success: true,
    Status: 'success',
    ErrorMessage: '',
    Mode: 'full',
    Source: 'api',
    ...overrides,
  }
}

test('call trace timeline explains aliased successful calls', () => {
  const items = buildCallTraceTimeline(baseRecord())

  assert.equal(items.length, 4)
  assert.equal(items[0].label, '接收请求')
  assert.equal(items[0].meta.find((item) => item.label === '工具')?.value, 'fs_read')
  assert.match(items[1].description, /fs_read/)
  assert.match(items[1].description, /read_file/)
  assert.equal(items[2].tone, 'success')
  assert.equal(items[3].tone, 'success')
})

test('call trace timeline marks slow upstream calls as warning', () => {
  const items = buildCallTraceTimeline(baseRecord({ LatencyMS: 3500 }))

  assert.equal(items[2].label, '调用上游')
  assert.equal(items[2].tone, 'warning')
  assert.equal(items[2].meta.find((item) => item.label === '耗时')?.value, '3,500 ms')
})

test('call trace timeline keeps upstream error distinct from gateway failure', () => {
  const items = buildCallTraceTimeline(
    baseRecord({
      Success: false,
      Status: 'upstream_error',
      ErrorMessage: '上游 MCP 返回错误结果',
      Source: 'xiaozhi',
      APIKeyID: '',
      APIKeyName: '',
      Mode: 'smart',
    }),
  )

  assert.equal(items[0].description, '小智接入 发起工具调用。')
  assert.equal(items[0].meta.find((item) => item.label === '模式')?.value, '智能模式')
  assert.equal(items[3].tone, 'warning')
  assert.match(items[3].description, /上游返回了错误结果/)
})
