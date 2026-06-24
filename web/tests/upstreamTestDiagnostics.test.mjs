import assert from 'node:assert/strict'
import test from 'node:test'
import {
  testStageLabel,
  upstreamTestDiagnostic,
} from '../src/utils/upstreamTestDiagnostics.ts'

test('labels upstream test stages', () => {
  assert.equal(testStageLabel('connect'), '连接阶段')
  assert.equal(testStageLabel('list_tools'), '工具列表')
  assert.equal(testStageLabel('ok'), '测试完成')
  assert.equal(testStageLabel('custom'), 'custom')
})

test('returns no diagnostic for successful upstream test', () => {
  assert.equal(
    upstreamTestDiagnostic({ ok: true, stage: 'ok', durationMs: 12, count: 1, tools: [] }, 'streamable-http'),
    null,
  )
})

test('diagnoses remote timeout with network actions', () => {
  const got = upstreamTestDiagnostic(
    { ok: false, stage: 'connect', durationMs: 50000, message: 'context deadline exceeded', count: 0, tools: [] },
    'streamable-http',
  )
  assert.equal(got?.title, '连接超时')
  assert.ok(got?.actions.some((item) => item.includes('网关所在机器')))
})

test('diagnoses authentication failure', () => {
  const got = upstreamTestDiagnostic(
    { ok: false, stage: 'connect', durationMs: 120, message: '401 unauthorized', count: 0, tools: [] },
    'sse',
  )
  assert.equal(got?.title, '认证未通过')
})

test('diagnoses list tools failure separately from connect failure', () => {
  const got = upstreamTestDiagnostic(
    { ok: false, stage: 'list_tools', durationMs: 180, message: 'schema error', count: 0, tools: [] },
    'websocket',
  )
  assert.equal(got?.title, '工具列表拉取失败')
})

test('diagnoses stdio startup failures', () => {
  const got = upstreamTestDiagnostic(
    { ok: false, stage: 'connect', durationMs: 80, message: 'exit status 1', count: 0, tools: [] },
    'stdio',
  )
  assert.equal(got?.title, '本地命令启动失败')
})
