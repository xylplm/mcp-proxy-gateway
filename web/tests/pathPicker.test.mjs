import assert from 'node:assert/strict'
import test from 'node:test'
import {
  appendUniquePath,
  breadcrumbParts,
  browseRootTone,
  displayPath,
  joinPathLines,
  parentPathHint,
  splitPathLines,
} from '../src/utils/pathPicker.ts'

test('split/join path lines', () => {
  assert.deepEqual(splitPathLines(' /a \n\nB\\c\n'), ['/a', 'B\\c'])
  assert.equal(joinPathLines(['/a', '', '/b']), '/a\n/b')
})

test('appendUniquePath avoids duplicates', () => {
  assert.equal(appendUniquePath('/a\n/b', '/a'), '/a\n/b')
  assert.equal(appendUniquePath('/a', '/b'), '/a\n/b')
  assert.equal(appendUniquePath('', '  /x  '), '/x')
})

test('displayPath normalizes separators', () => {
  assert.equal(displayPath('C:\\data\\ws', '/'), 'C:/data/ws')
  assert.equal(displayPath('/data/ws', '\\'), '\\data\\ws')
})

test('parentPathHint', () => {
  assert.equal(parentPathHint('/data/ws/demo'), '/data/ws')
  assert.equal(parentPathHint('C:\\data\\ws'), 'C:\\data')
  assert.equal(parentPathHint('/'), '')
})

test('breadcrumbParts unix and windows', () => {
  assert.deepEqual(breadcrumbParts('/data/ws'), ['/', 'data', 'ws'])
  assert.deepEqual(breadcrumbParts('C:\\data\\ws', '\\'), ['C:\\', 'data', 'ws'])
  assert.deepEqual(breadcrumbParts('/'), ['/'])
})

test('browseRootTone covers kinds', () => {
  assert.match(browseRootTone('data'), /brand/)
  assert.match(browseRootTone('runtime'), /success/)
  assert.match(browseRootTone('global_file'), /warning/)
  assert.match(browseRootTone('extra'), /gray/)
  assert.match(browseRootTone('context'), /gray/)
})
