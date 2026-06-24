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

test('does not mark read-only media status as send risk', () => {
  const tags = toolRiskTags(
    detail({
      name: 'get_media_status',
      description: '查询媒体状态，例如：已发布站点列表、是否已入库等。',
    }),
  )

  assert.equal(tags.some((tag) => tag.key === 'send'), false)
})

test('does not mark query message tools as send risk', () => {
  const tags = toolRiskTags(detail({ name: 'get_site_message', description: '查询站点消息' }))

  assert.equal(tags.some((tag) => tag.key === 'send'), false)
})

test('does not infer risk from historical state words in read-only tools', () => {
  const cases = [
    { name: 'get_paid_orders', description: '查询已支付订单' },
    { name: 'get_sent_message', description: '获取已发送的消息内容' },
    { name: 'view_written_content', description: '查看已写入的内容' },
  ]

  for (const item of cases) {
    assert.deepEqual(
      toolRiskTags(detail(item)).map((tag) => tag.key),
      [],
      item.name,
    )
  }
})

test('marks explicit follow-up actions after read-only wording', () => {
  const sendTags = toolRiskTags(detail({ name: 'get_and_send_report', description: '查询并发送报告' }))
  const deleteTags = toolRiskTags(detail({ name: 'read_then_delete_file', description: '读取后删除文件' }))

  assert.deepEqual(
    sendTags.map((tag) => tag.key),
    ['send'],
  )
  assert.deepEqual(
    deleteTags.map((tag) => tag.key),
    ['delete'],
  )
})

test('keeps current media catalog query tools untagged', () => {
  const readOnlyTools = [
    { name: 'get_media_servers', description: '查询媒体服务列表' },
    { name: 'get_media_status', description: '查询媒体状态，例如：已发布站点列表、是否已入库等。' },
    { name: 'get_site_message', description: '查询站点消息' },
    { name: 'today_site_data', description: '查询站点数据今日汇总，例如：总上传量、总下载量' },
    { name: 'total_site_data', description: '查询站点总数据，例如：上传、下载、魔力、做种数、做种体积等统计数据信息。' },
  ]

  for (const item of readOnlyTools) {
    assert.deepEqual(
      toolRiskTags(detail(item)).map((tag) => tag.key),
      [],
      item.name,
    )
  }
})

test('marks current media catalog mutating tools without payment false positives', () => {
  const expected = [
    ['add_offline_download', '添加云下载任务', ['write']],
    ['cloud_share_receive', '云存储转存', ['write']],
    ['refresh_subscribe', '刷新订阅列表, 立即开始搜索、下载电影或电视剧', ['write']],
    ['site_sign_in', '站点签到', ['write']],
    ['subscribe_movie', '订阅电影, 订阅成功后会自动搜索、下载电影', ['write']],
    ['sync_site_message', '同步站点消息', ['write']],
    ['update_site_configs', '更新站点适配文件', ['write']],
  ]

  for (const [name, description, tagKeys] of expected) {
    assert.deepEqual(
      toolRiskTags(detail({ name, description })).map((tag) => tag.key),
      tagKeys,
      name,
    )
  }
})

test('does not let a read-only alias hide a mutating original name', () => {
  const tags = toolRiskTags(detail({ name: 'get_cleanup_status', originalName: 'delete_temp_files', description: '查看清理状态' }))

  assert.deepEqual(
    tags.map((tag) => tag.key),
    ['delete'],
  )
})

test('merges custom policy risk tags without raising severity', () => {
  const item = detail({ name: 'read_file', description: '读取文件内容' })
  item.policy = {
    ruleId: 'policy-1',
    pattern: 'read_file',
    cacheEnabled: false,
    riskTags: ['内部数据', '内部数据'],
  }

  const tags = toolRiskTags(item)
  assert.deepEqual(
    tags.map((tag) => tag.key),
    ['custom:内部数据'],
  )
  assert.equal(highestRiskLevel(tags), 'low')
})

test('ignores selected automatic policy risk tags', () => {
  const item = detail({ name: 'send_message', description: '发布通知并更新状态' })
  item.policy = {
    ruleId: 'policy-1',
    pattern: 'send_message',
    cacheEnabled: false,
    ignoredRiskTags: ['send'],
  }

  assert.deepEqual(
    toolRiskTags(item).map((tag) => tag.key),
    ['write'],
  )
})

test('restores automatic risk tags when ignored keys are cleared', () => {
  const item = detail({ name: 'send_message', description: '发布通知并更新状态' })
  item.policy = {
    ruleId: 'policy-1',
    pattern: 'send_message',
    cacheEnabled: false,
    ignoredRiskTags: [],
  }

  assert.deepEqual(
    toolRiskTags(item).map((tag) => tag.key),
    ['write', 'send'],
  )
})

test('keeps custom risk tags after ignoring automatic tags', () => {
  const item = detail({ name: 'send_message', description: '发布通知并更新状态' })
  item.policy = {
    ruleId: 'policy-1',
    pattern: 'send_message',
    cacheEnabled: false,
    riskTags: ['内部数据'],
    ignoredRiskTags: ['send'],
  }

  assert.deepEqual(
    toolRiskTags(item).map((tag) => tag.key),
    ['write', 'custom:内部数据'],
  )
})
