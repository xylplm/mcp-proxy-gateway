import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildToolCountSnapshot,
  toolChangeSummaryLabel,
  toolCountLabel,
} from '../src/utils/toolCountSnapshot.ts'

test('tool count label keeps unknown and unsynced states distinct', () => {
  assert.equal(toolCountLabel(), '工具 —')
  assert.equal(toolCountLabel(buildToolCountSnapshot(0, null)), '工具 未同步')
  assert.equal(toolCountLabel(buildToolCountSnapshot(0, '2026-06-23T10:00:00Z')), '工具 0')
  assert.equal(toolCountLabel(buildToolCountSnapshot(12, '2026-06-23T10:00:00Z')), '工具 12')
})

test('tool change summary label keeps the latest sync compact', () => {
  assert.equal(toolChangeSummaryLabel(), '暂无同步变化')
  assert.equal(
    toolChangeSummaryLabel(buildToolCountSnapshot(12, '2026-06-23T10:00:00Z', {
      added: 0,
      removed: 0,
      schemaChanged: 0,
      syncedAt: '2026-06-23T10:00:00Z',
    })),
    '本次同步无变化',
  )
  assert.equal(
    toolChangeSummaryLabel(buildToolCountSnapshot(12, '2026-06-23T10:00:00Z', {
      added: 2,
      removed: 1,
      schemaChanged: 3,
      syncedAt: '2026-06-23T10:00:00Z',
    })),
    '新增 2，移除 1，Schema 变更 3',
  )
})
