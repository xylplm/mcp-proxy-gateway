import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildToolRulePreview,
  createOriginalNameMatcher,
  scopedEnabledUpstreamIDs,
} from '../src/utils/rulePreviewCore.ts'

function upstream(id, name, enabled = true) {
  return {
    id,
    state: 'available',
    config: {
      name,
      enabled,
      sortOrder: 0,
      transport: 'streamable-http',
      connParams: {},
      autoSync: true,
    },
    createdAt: '',
    updatedAt: '',
  }
}

function tool(upstreamId, originalName, name = originalName) {
  return {
    upstreamId,
    originalName,
    name,
    description: '',
    inputSchema: { type: 'object' },
    order: 0,
  }
}

test('scopes preview to enabled upstreams only', () => {
  const ups = [upstream('up-a', 'A'), upstream('up-b', 'B', false), upstream('up-c', 'C')]

  assert.deepEqual(scopedEnabledUpstreamIDs({ scopeType: 'all' }, ups), ['up-a', 'up-c'])
  assert.deepEqual(
    scopedEnabledUpstreamIDs({ scopeType: 'upstreams', upstreamIds: ['up-b', 'up-c'] }, ups),
    ['up-c'],
  )
})

test('matches original tool names with exact and regex modes', () => {
  assert.equal(createOriginalNameMatcher('read_file', false)?.('read_file'), true)
  assert.equal(createOriginalNameMatcher('read_file', false)?.('read_file_meta'), false)
  assert.equal(createOriginalNameMatcher('read_.+', true)?.('read_file'), true)
  assert.equal(createOriginalNameMatcher('[', true), null)
})

test('builds rule preview from cached tools', () => {
  const ups = [upstream('up-a', '文件'), upstream('up-b', '消息')]
  const summary = buildToolRulePreview(
    { scopeType: 'all', pattern: 'read_.+', isRegex: true },
    ups,
    {
      'up-a': [tool('up-a', 'read_file', 'fs_read'), tool('up-a', 'delete_file')],
      'up-b': [tool('up-b', 'read_message')],
    },
    { limit: 1, hitLabel: (count) => `命中 ${count}` },
  )

  assert.equal(summary.label, '命中 2')
  assert.equal(summary.totalCount, 2)
  assert.equal(summary.items.length, 1)
  assert.equal(summary.hiddenCount, 1)
  assert.equal(summary.items[0].upstreamName, '文件')
})

test('can preview by exposed tool name for tool policies', () => {
  const ups = [upstream('up-a', '文件')]
  const summary = buildToolRulePreview(
    { scopeType: 'all', pattern: 'fs_read', isRegex: false },
    ups,
    {
      'up-a': [tool('up-a', 'read_file', 'fs_read'), tool('up-a', 'fs_read', 'raw_fs_read')],
    },
    { matchField: 'exposedName' },
  )

  assert.equal(summary.totalCount, 1)
  assert.equal(summary.items[0].originalName, 'read_file')
  assert.equal(summary.items[0].exposedName, 'fs_read')
})

test('keeps empty draft preview gentle', () => {
  const summary = buildToolRulePreview(
    { scopeType: 'all', pattern: ' ', isRegex: false },
    [upstream('up-a', 'A')],
    { 'up-a': [tool('up-a', 'read')] },
  )

  assert.equal(summary.label, '填写匹配模式后显示预计影响')
  assert.equal(summary.totalCount, 0)
})
