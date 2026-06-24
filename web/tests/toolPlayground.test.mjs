import assert from 'node:assert/strict'
import test from 'node:test'
import { parsePlaygroundArgs, prettifyPlaygroundValue } from '../src/utils/toolPlayground.ts'

test('parses empty playground arguments as object', () => {
  const got = parsePlaygroundArgs('   ')

  assert.equal(got.ok, true)
  assert.deepEqual(got.value, {})
})

test('parses valid JSON object playground arguments', () => {
  const got = parsePlaygroundArgs('{"path":"README.md","limit":3}')

  assert.equal(got.ok, true)
  assert.deepEqual(got.value, { path: 'README.md', limit: 3 })
})

test('rejects non-object playground arguments', () => {
  const got = parsePlaygroundArgs('[1,2,3]')

  assert.equal(got.ok, false)
  assert.equal(got.error, '入参必须是 JSON 对象')
})

test('rejects malformed playground arguments', () => {
  const got = parsePlaygroundArgs('{"path":')

  assert.equal(got.ok, false)
  assert.equal(got.error, '入参不是合法 JSON')
})

test('prettifies playground values', () => {
  assert.equal(prettifyPlaygroundValue({ ok: true }), '{\n  "ok": true\n}')
  assert.equal(prettifyPlaygroundValue('{"ok":true}'), '{\n  "ok": true\n}')
})
