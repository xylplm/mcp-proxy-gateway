import assert from 'node:assert/strict'
import test from 'node:test'
import { buildAPIKeyFilterPreview } from '../src/utils/apiKeyFilterPreview.ts'

function toolDetail(name, sources) {
  return {
    tool: {
      upstreamId: sources[0]?.upstreamId ?? '',
      originalName: sources[0]?.originalName ?? name,
      name,
      description: '',
      inputSchema: { type: 'object' },
      order: 0,
    },
    sources,
  }
}

function source(upstreamId, upstreamName, originalName) {
  return {
    upstreamId,
    upstreamName,
    originalName,
    description: '',
    inputSchema: { type: 'object' },
    compatible: true,
    schemaConflict: false,
  }
}

test('keeps empty API key filter draft preview quiet', () => {
  const summary = buildAPIKeyFilterPreview(
    { pattern: ' ', isRegex: false, enabled: true },
    [toolDetail('read_file', [source('up-a', '文件', 'read_file')])],
    true,
  )

  assert.equal(summary.label, '填写匹配模式后显示预计影响')
  assert.equal(summary.totalCount, 0)
  assert.deepEqual(summary.items, [])
})

test('previews API key filter hits across aggregated sources', () => {
  const summary = buildAPIKeyFilterPreview(
    { pattern: 'read_.+', isRegex: true, enabled: true },
    [
      toolDetail('read_file', [
        source('up-a', '文件 A', 'read_file'),
        source('up-b', '文件 B', 'read_file'),
      ]),
      toolDetail('send_message', [source('up-c', '消息', 'send_message')]),
      toolDetail('read_message', [source('up-c', '消息', 'read_message')]),
    ],
    true,
    { limit: 2, hitLabel: (count) => `预计屏蔽 ${count} 个来源` },
  )

  assert.equal(summary.label, '预计屏蔽 3 个来源')
  assert.equal(summary.totalCount, 3)
  assert.equal(summary.items.length, 2)
  assert.equal(summary.hiddenCount, 1)
  assert.equal(summary.items[0].upstreamName, '文件 A')
  assert.equal(summary.items[1].upstreamName, '文件 B')
})

test('handles unavailable data and invalid regex without throwing', () => {
  const detail = [toolDetail('read_file', [source('up-a', '文件', 'read_file')])]

  assert.equal(
    buildAPIKeyFilterPreview({ pattern: 'read_file', isRegex: false }, detail, false).label,
    '当前聚合来源暂不可用',
  )
  assert.equal(
    buildAPIKeyFilterPreview({ pattern: '[', isRegex: true }, detail, true).label,
    '匹配模式暂不可用',
  )
})

test('returns disabled label for disabled saved filters', () => {
  const summary = buildAPIKeyFilterPreview(
    { pattern: 'read_file', isRegex: false, enabled: false },
    [toolDetail('read_file', [source('up-a', '文件', 'read_file')])],
    true,
  )

  assert.equal(summary.label, '规则未启用')
  assert.equal(summary.totalCount, 0)
})
