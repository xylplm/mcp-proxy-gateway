import assert from 'node:assert/strict'
import test from 'node:test'
import {
  UPSTREAM_NAME_MAX_RUNES,
  buildUpstreamCloneFormSource,
  suggestUniqueUpstreamName,
} from '../src/utils/upstreamCopy.ts'

function upstream(overrides = {}) {
  return {
    id: 'up-src',
    config: {
      name: 'OpenAI 官方',
      tags: ['AI', 'ai', '  '],
      transport: 'streamable-http',
      connParams: {
        url: 'https://example.com/mcp',
        headers: { Authorization: 'Bearer ${credential}' },
        args: ['a'],
      },
      credential: 'sk-secret',
      enabled: true,
      sortOrder: 7,
      autoSync: false,
      rateLimits: { enabled: true, perMinute: 60, timezone: 'Asia/Shanghai' },
    },
    state: 'available',
    lastError: 'old error',
    createdAt: '2026-06-24T08:00:00Z',
    updatedAt: '2026-06-24T09:00:00Z',
    ...overrides,
  }
}

test('suggestUniqueUpstreamName appends 副本 and increments', () => {
  assert.equal(suggestUniqueUpstreamName('GitHub', []), 'GitHub 副本')
  assert.equal(suggestUniqueUpstreamName('GitHub', ['GitHub 副本']), 'GitHub 副本2')
  assert.equal(
    suggestUniqueUpstreamName('GitHub', ['GitHub 副本', 'GitHub 副本2', 'github 副本3']),
    'GitHub 副本4',
  )
})

test('suggestUniqueUpstreamName respects 100-rune limit and empty source', () => {
  const long = '测'.repeat(98)
  const name = suggestUniqueUpstreamName(long, [])
  assert.ok([...name].length <= UPSTREAM_NAME_MAX_RUNES)
  assert.match(name, /副本$/)

  assert.equal(suggestUniqueUpstreamName('   ', ['上游 副本']), '上游 副本2')
})

test('buildUpstreamCloneFormSource deep-copies editable fields and drops runtime identity', () => {
  const source = upstream()
  const clone = buildUpstreamCloneFormSource(source, 'OpenAI 官方 副本')

  assert.equal(clone.name, 'OpenAI 官方 副本')
  assert.deepEqual(clone.tags, ['AI'])
  assert.equal(clone.transport, 'streamable-http')
  assert.equal(clone.credential, 'sk-secret')
  assert.equal(clone.enabled, true)
  assert.equal(clone.autoSync, false)
  assert.equal(clone.rateLimits.perMinute, 60)
  assert.equal(clone.sourceId, 'up-src')
  assert.equal(clone.sourceName, 'OpenAI 官方')
  assert.equal('id' in clone, false)
  assert.equal('state' in clone, false)
  assert.equal('sortOrder' in clone, false)

  // 深拷贝：修改 clone 不影响源对象
  clone.connParams.url = 'https://changed.example/mcp'
  clone.connParams.headers.Authorization = 'changed'
  clone.connParams.args.push('b')
  clone.rateLimits.perMinute = 1
  clone.tags.push('extra')

  assert.equal(source.config.connParams.url, 'https://example.com/mcp')
  assert.equal(source.config.connParams.headers.Authorization, 'Bearer ${credential}')
  assert.deepEqual(source.config.connParams.args, ['a'])
  assert.equal(source.config.rateLimits.perMinute, 60)
  assert.deepEqual(source.config.tags, ['AI', 'ai', '  '])
})

test('buildUpstreamCloneFormSource tolerates missing connParams and unknown transport', () => {
  const clone = buildUpstreamCloneFormSource(
    {
      id: 'broken',
      config: {
        name: '残缺配置',
        tags: null,
        transport: 'unknown-transport',
        connParams: null,
        credential: '',
        enabled: false,
        autoSync: true,
      },
    },
    '残缺配置 副本',
  )

  assert.equal(clone.transport, 'stdio')
  assert.deepEqual(clone.connParams, {})
  assert.deepEqual(clone.tags, [])
  assert.equal(clone.enabled, false)
  assert.equal(clone.rateLimits.enabled, false)
})
