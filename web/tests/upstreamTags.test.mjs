import assert from 'node:assert/strict'
import test from 'node:test'
import {
  collectAllTags,
  countUntagged,
  filterUpstreamsByTag,
  groupUpstreamsByTag,
  isUntagged,
  matchTag,
  normalizeTags,
} from '../src/utils/upstreamTags.ts'

function up(name, tags) {
  return { id: name, config: { name, tags } }
}

test('normalizeTags trims, drops empty, and de-duplicates case-insensitively', () => {
  assert.deepEqual(normalizeTags(['  AI ', 'ai', 'Search', '', '  ', 'search', 'Dev']), [
    'AI',
    'Search',
    'Dev',
  ])
  assert.deepEqual(normalizeTags(null), [])
  assert.deepEqual(normalizeTags(undefined), [])
  assert.deepEqual(normalizeTags([]), [])
})

test('matchTag and isUntagged are case-insensitive', () => {
  const item = up('a', ['OpenAI', '  '])
  assert.equal(matchTag(item, 'openai'), true)
  assert.equal(matchTag(item, 'OPENAI'), true)
  assert.equal(matchTag(item, 'missing'), false)
  assert.equal(isUntagged(item), false)
  assert.equal(isUntagged(up('b', [])), true)
  assert.equal(isUntagged(up('c', ['  ', ''])), true)
  assert.equal(isUntagged(up('d', null)), true)
})

test('collectAllTags counts membership and sorts by frequency then name', () => {
  const list = [up('a', ['AI', 'Search']), up('b', ['ai', 'Dev']), up('c', ['search']), up('d', [])]
  assert.deepEqual(collectAllTags(list), [
    { tag: 'AI', count: 2 },
    { tag: 'Search', count: 2 },
    { tag: 'Dev', count: 1 },
  ])
  assert.equal(countUntagged(list), 1)
})

test('filterUpstreamsByTag supports all / untagged / specific tag', () => {
  const list = [up('a', ['AI']), up('b', ['Search']), up('c', []), up('d', null)]
  assert.equal(filterUpstreamsByTag(list, 'all').length, 4)
  assert.deepEqual(
    filterUpstreamsByTag(list, 'untagged').map((item) => item.id),
    ['c', 'd'],
  )
  assert.deepEqual(
    filterUpstreamsByTag(list, 'ai').map((item) => item.id),
    ['a'],
  )
})

test('groupUpstreamsByTag puts multi-tag upstreams in multiple groups and untagged last', () => {
  const multi = up('multi', ['Alpha', 'Beta'])
  const only = up('only', ['Alpha'])
  const bare = up('bare', [])
  const groups = groupUpstreamsByTag([multi, only, bare])

  assert.equal(groups.length, 3)
  assert.equal(groups[0].label, 'Alpha')
  assert.deepEqual(
    groups[0].items.map((item) => item.id),
    ['multi', 'only'],
  )
  assert.equal(groups[1].label, 'Beta')
  assert.deepEqual(
    groups[1].items.map((item) => item.id),
    ['multi'],
  )
  assert.equal(groups[2].key, 'untagged')
  assert.deepEqual(
    groups[2].items.map((item) => item.id),
    ['bare'],
  )
})

test('filter and group tolerate malformed upstream shapes', () => {
  const broken = [{ id: 'x' }, { id: 'y', config: null }, up('z', ['OK'])]
  assert.equal(filterUpstreamsByTag(broken, 'all').length, 3)
  assert.deepEqual(
    filterUpstreamsByTag(broken, 'untagged').map((item) => item.id),
    ['x', 'y'],
  )
  assert.deepEqual(
    filterUpstreamsByTag(broken, 'ok').map((item) => item.id),
    ['z'],
  )
  const groups = groupUpstreamsByTag(broken)
  assert.equal(groups[0].label, 'OK')
  assert.equal(groups[1].key, 'untagged')
})

test('filtering by one tag then grouping should not invent empty semantics', () => {
  // 工具函数本身按全量标签分区；页面层在单标签筛选时改为单分区。
  // 这里锁定工具函数语义：多标签上游会进入多个分区。
  const item = up('multi', ['AI', 'Search'])
  const groups = groupUpstreamsByTag([item])
  assert.equal(groups.length, 2)
  assert.deepEqual(
    groups.map((g) => g.label).sort(),
    ['AI', 'Search'],
  )
})
