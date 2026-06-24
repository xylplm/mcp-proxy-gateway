import assert from 'node:assert/strict'
import test from 'node:test'
import {
  cachePolicyLabel,
  normalizePolicyRiskTags,
  routingStrategyLabel,
  toolPolicyToRequest,
} from '../src/utils/toolPolicy.ts'

test('normalizes custom risk tags', () => {
  assert.deepEqual(normalizePolicyRiskTags([' 写入 ', '发送', '写入', '', '支付']), ['写入', '发送', '支付'])
})

test('builds compact tool policy request', () => {
  const payload = toolPolicyToRequest({
    id: 'policy-1',
    pattern: 'read',
    isRegex: false,
    enabled: true,
    sortOrder: 3,
    routingStrategy: 'priority_fill',
    cacheEnabled: true,
    cacheTtlSeconds: 0,
    riskTags: [' 标签 ', '标签'],
  })

  assert.equal(payload.cacheTtlSeconds, 1)
  assert.equal(payload.routingStrategy, 'priority_fill')
  assert.deepEqual(payload.riskTags, ['标签'])
})

test('formats routing and cache labels', () => {
  assert.equal(routingStrategyLabel(''), '不覆盖')
  assert.equal(routingStrategyLabel('round_robin'), '轮询')
  assert.equal(cachePolicyLabel({ cacheEnabled: false }), '未启用缓存')
  assert.equal(cachePolicyLabel({ cacheEnabled: true, cacheTtlSeconds: 30 }), '缓存 30 秒')
})
