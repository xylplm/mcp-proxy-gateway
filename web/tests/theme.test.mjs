import assert from 'node:assert/strict'
import test from 'node:test'
import { parseThemeMode, resolveTheme } from '../src/utils/theme.ts'

test('新用户和无效旧设置默认跟随系统', () => {
  assert.equal(parseThemeMode(null), 'system')
  assert.equal(parseThemeMode(''), 'system')
  assert.equal(parseThemeMode('unknown'), 'system')
})

test('兼容旧的 light 和 dark 设置', () => {
  assert.equal(parseThemeMode('light'), 'light')
  assert.equal(parseThemeMode('dark'), 'dark')
  assert.equal(parseThemeMode('system'), 'system')
})

test('仅跟随系统模式响应系统明暗变化', () => {
  assert.equal(resolveTheme('system', false), 'light')
  assert.equal(resolveTheme('system', true), 'dark')
  assert.equal(resolveTheme('light', true), 'light')
  assert.equal(resolveTheme('dark', false), 'dark')
})
