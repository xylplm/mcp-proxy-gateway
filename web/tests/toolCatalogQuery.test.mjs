import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildToolCatalogQuery,
  parseToolCatalogQuery,
  sameToolCatalogQuery,
} from '../src/utils/toolCatalogQuery.ts'

test('parses tool catalog query with valid filters', () => {
  assert.deepEqual(
    parseToolCatalogQuery({
      apiKeyId: ' key-1 ',
      view: 'attention',
      q: ' search ',
      upstreamId: ' up-1 ',
      status: 'conflict',
      risk: 'risk',
      tool: ' read_file ',
    }),
    {
      apiKeyId: 'key-1',
      view: 'attention',
      search: 'search',
      upstreamId: 'up-1',
      status: 'conflict',
      risk: 'risk',
      tool: 'read_file',
    },
  )
})

test('falls back invalid tool catalog query filters to all', () => {
  assert.deepEqual(
    parseToolCatalogQuery({
      status: 'broken',
      risk: ['unknown', 'risk'],
      view: 'unknown',
      q: ['first', 'second'],
    }),
    {
      apiKeyId: '',
      view: 'all',
      search: 'first',
      upstreamId: '',
      status: 'all',
      risk: 'all',
      tool: '',
    },
  )
})

test('builds compact query without default values', () => {
  assert.deepEqual(
    buildToolCatalogQuery({
      apiKeyId: ' key-1 ',
      view: 'risk',
      search: ' media ',
      upstreamId: '',
      status: 'all',
      risk: 'risk',
      tool: '',
    }),
    {
      apiKeyId: 'key-1',
      view: 'risk',
      q: 'media',
      risk: 'risk',
    },
  )
})

test('compares tool catalog query independent of key order', () => {
  assert.equal(
    sameToolCatalogQuery(
      { q: 'media', risk: 'risk', tool: 'get_media_status' },
      { tool: 'get_media_status', q: 'media', risk: 'risk' },
    ),
    true,
  )
  assert.equal(sameToolCatalogQuery({ q: 'media' }, { q: 'media', risk: 'risk' }), false)
})
