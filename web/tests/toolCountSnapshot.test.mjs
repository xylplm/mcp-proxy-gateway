import assert from 'node:assert/strict'
import test from 'node:test'
import { buildToolCountSnapshot, toolCountLabel } from '../src/utils/toolCountSnapshot.ts'

test('tool count label keeps unknown and unsynced states distinct', () => {
  assert.equal(toolCountLabel(), '工具 —')
  assert.equal(toolCountLabel(buildToolCountSnapshot(0, null)), '工具 未同步')
  assert.equal(toolCountLabel(buildToolCountSnapshot(0, '2026-06-23T10:00:00Z')), '工具 0')
  assert.equal(toolCountLabel(buildToolCountSnapshot(12, '2026-06-23T10:00:00Z')), '工具 12')
})
