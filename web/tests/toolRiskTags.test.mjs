import assert from 'node:assert/strict'
import test from 'node:test'
import { highestRiskLevel, toolRiskTags } from '../src/utils/toolRiskTags.ts'

function detail(tool) {
  return {
    tool: {
      originalName: tool.originalName ?? tool.name,
      name: tool.name,
      description: tool.description ?? '',
      inputSchema: { type: 'object' },
      upstreamId: 'up-1',
      order: 0,
      sourceCount: 1,
      schemaConflict: false,
    },
    sources: [
      {
        upstreamId: 'up-1',
        upstreamName: '默认上游',
        originalName: tool.originalName ?? tool.name,
        description: tool.description ?? '',
        inputSchema: { type: 'object' },
        compatible: true,
        schemaConflict: false,
        rateLimits: { enabled: false },
      },
    ],
  }
}

test('marks destructive and payment tools as high risk', () => {
  const tags = toolRiskTags(detail({ name: 'delete_invoice', description: '删除并退款账单' }))

  assert.deepEqual(
    tags.map((tag) => tag.key),
    ['payment', 'delete'],
  )
  assert.equal(highestRiskLevel(tags), 'high')
})

test('marks write and send tools as medium risk', () => {
  const tags = toolRiskTags(detail({ name: 'send_message', description: '发布通知并更新状态' }))

  assert.deepEqual(
    tags.map((tag) => tag.key),
    ['write', 'send'],
  )
  assert.equal(highestRiskLevel(tags), 'medium')
})

test('keeps read-only tools untagged', () => {
  const tags = toolRiskTags(detail({ name: 'read_file', description: '读取文件内容' }))

  assert.equal(tags.length, 0)
  assert.equal(highestRiskLevel(tags), null)
})

test('does not mark media subscription tools as payment risk', () => {
  const subscribeTags = toolRiskTags(detail({ name: 'subscribe_movie', description: '订阅电影，成功后自动搜索下载' }))
  const refreshTags = toolRiskTags(detail({ name: 'refresh_subscribe', description: '刷新订阅列表' }))

  assert.equal(subscribeTags.some((tag) => tag.key === 'payment'), false)
  assert.equal(refreshTags.some((tag) => tag.key === 'payment'), false)
})
