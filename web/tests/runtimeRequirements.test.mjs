import assert from 'node:assert/strict'
import test from 'node:test'
import {
  inferToolsFromCommand,
  inferToolsFromTemplateRuntimes,
  normalizeRequirements,
  preflightReadyLabel,
  preflightTone,
  resolveEffectiveTools,
} from '../src/utils/runtimeRequirements.ts'

test('inferToolsFromCommand covers common launchers', () => {
  assert.deepEqual(inferToolsFromCommand('npx'), ['node', 'npx'])
  assert.deepEqual(inferToolsFromCommand('/usr/bin/python3'), ['python3'])
  assert.deepEqual(inferToolsFromCommand('uvx'), ['uv', 'uvx'])
  assert.deepEqual(inferToolsFromCommand('D:\\\\app\\\\mcp.exe'), [])
})

test('resolveEffectiveTools respects manual and auto fallback', () => {
  const auto = resolveEffectiveTools('npx', { mode: 'auto', tools: [] })
  assert.deepEqual(auto.effective, ['node', 'npx'])
  const manual = resolveEffectiveTools('npx', { mode: 'manual', tools: ['docker'] })
  assert.deepEqual(manual.effective, ['docker'])
  const fallback = resolveEffectiveTools('D:/x.exe', { mode: 'auto', tools: ['node'] })
  assert.deepEqual(fallback.effective, ['node'])
})

test('normalizeRequirements and labels', () => {
  assert.deepEqual(normalizeRequirements(null), { mode: 'auto', tools: [] })
  assert.equal(preflightReadyLabel(true, true, true), '依赖已就绪')
  assert.equal(preflightTone(false, true, true), 'warning')
  assert.equal(preflightTone(false, false, true), 'error')
  assert.deepEqual(inferToolsFromTemplateRuntimes(['node', 'docker']), ['node', 'npx', 'docker'])
})
