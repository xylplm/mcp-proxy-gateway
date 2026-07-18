import assert from 'node:assert/strict'
import test from 'node:test'
import {
  aliasEffectLabel,
  buildRuleMatchPreview,
  toolPolicyEffectLabel,
} from '../src/utils/ruleMatchPreview.ts'

const upstreams = [
  { id: 'up-a', config: { name: 'A 服务', enabled: true } },
  { id: 'up-b', config: { name: 'B 服务', enabled: true } },
  { id: 'up-off', config: { name: '停用服务', enabled: false } },
]

test('previews filters before alias and policy rules', () => {
  const preview = buildRuleMatchPreview({
    query: 'delete_file',
    upstreams,
    toolsByUpstream: {
      'up-a': [{ originalName: 'delete_file', name: 'delete_file', description: '', inputSchema: {}, upstreamId: 'up-a', order: 0 }],
    },
    filters: [{ id: 'filter-1', pattern: 'delete_file', isRegex: false, enabled: true, sortOrder: 0, scopeType: 'all' }],
    aliases: [{ id: 'alias-1', pattern: 'delete_file', isRegex: false, targetName: 'safe_delete', targetDesc: '', sortOrder: 0, scopeType: 'all' }],
    toolPolicies: [{ id: 'policy-1', pattern: 'safe_delete', isRegex: false, enabled: true, sortOrder: 0, routingStrategy: 'round_robin', cacheEnabled: true, cacheTtlSeconds: 30 }],
  })

  assert.equal(preview.visibleCount, 0)
  assert.equal(preview.filteredCount, 1)
  assert.equal(preview.sources[0].filterRule.id, 'filter-1')
  assert.equal(preview.sources[0].aliasRule, undefined)
  assert.equal(preview.sources[0].policyRule, undefined)
})

test('previews alias output name as tool policy match input', () => {
  const preview = buildRuleMatchPreview({
    query: 'raw_search',
    upstreams,
    toolsByUpstream: {
      'up-a': [{ originalName: 'raw_search', name: 'raw_search', description: '', inputSchema: {}, upstreamId: 'up-a', order: 0 }],
      'up-b': [{ originalName: 'raw_search', name: 'raw_search', description: '', inputSchema: {}, upstreamId: 'up-b', order: 0 }],
      'up-off': [{ originalName: 'raw_search', name: 'raw_search', description: '', inputSchema: {}, upstreamId: 'up-off', order: 0 }],
    },
    filters: [],
    aliases: [{ id: 'alias-1', pattern: 'raw_search', isRegex: false, targetName: 'search', targetDesc: '搜索', sortOrder: 0, scopeType: 'upstreams', upstreamIds: ['up-a'] }],
    toolPolicies: [
      { id: 'policy-off', pattern: 'search', isRegex: false, enabled: false, sortOrder: 0, routingStrategy: 'priority_fill', cacheEnabled: false },
      { id: 'policy-1', pattern: 'search', isRegex: false, enabled: true, sortOrder: 1, routingStrategy: 'round_robin', cacheEnabled: true, cacheTtlSeconds: 60, riskTags: ['搜索'] },
    ],
  })

  assert.equal(preview.visibleCount, 2)
  assert.equal(preview.filteredCount, 0)
  assert.equal(preview.sources.length, 2)
  assert.equal(preview.sources[0].exposedName, 'search')
  assert.equal(preview.sources[0].aliasRule.id, 'alias-1')
  assert.equal(preview.sources[0].policyRule.id, 'policy-1')
  assert.equal(preview.sources[1].aliasRule, undefined)
  assert.equal(preview.sources[1].policyRule, undefined)
})

test('formats preview effect labels for users', () => {
  assert.equal(aliasEffectLabel({ targetName: 'search', targetDesc: '搜索' }), '改名为 search，并重写描述')
  assert.equal(toolPolicyEffectLabel({
    id: 'policy-1',
    pattern: 'search',
    isRegex: false,
    enabled: true,
    sortOrder: 0,
    routingStrategy: 'round_robin',
    cacheEnabled: true,
    cacheTtlSeconds: 30,
    riskTags: ['发送'],
  }), '智能均衡，缓存 30 秒，标签 发送')
})
