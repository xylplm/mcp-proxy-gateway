import assert from 'node:assert/strict'
import test from 'node:test'
import { highlightScript } from '../src/utils/scriptHighlight.ts'

test('highlightScript escapes html and marks keywords', () => {
  const html = highlightScript('print("<hi>")\n# cmt', 'python')
  assert.match(html, /&lt;hi&gt;/)
  assert.match(html, /tok-kw/)
  assert.match(html, /tok-comment/)
  assert.match(html, /script-ln/)
})

test('highlightScript handles javascript comments', () => {
  const html = highlightScript('// x\nconst a = 1', 'javascript')
  assert.match(html, /tok-comment/)
  assert.match(html, /tok-kw/)
})

test('highlightScript never lets source create html nodes', () => {
  const html = highlightScript(`const x = '<img src=x onerror=alert(1)>'`, 'javascript')
  assert.doesNotMatch(html, /<img/)
  assert.match(html, /&lt;img/)
})
