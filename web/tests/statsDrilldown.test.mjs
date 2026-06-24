import assert from 'node:assert/strict'
import test from 'node:test'
import {
  statsRangeQuery,
  statsToolCallsQuery,
  statsToolFailuresQuery,
} from '../src/utils/statsDrilldown.ts'

const range = {
  start: '2026-06-24T01:00:00.000Z',
  end: '2026-06-24T02:00:00.000Z',
  tz: 'UTC',
}

const tool = {
  UpstreamID: 'up-1',
  OriginalName: 'search_media',
}

test('builds call record range query from statistics range', () => {
  assert.deepEqual(statsRangeQuery(range), {
    since: '2026-06-24T01:00:00.000Z',
    until: '2026-06-24T02:00:00.000Z',
  })
})

test('omits empty range values', () => {
  assert.deepEqual(statsRangeQuery({ start: '', end: undefined, tz: 'UTC' }), {})
})

test('builds tool call drilldown query', () => {
  assert.deepEqual(statsToolCallsQuery(range, tool), {
    since: '2026-06-24T01:00:00.000Z',
    until: '2026-06-24T02:00:00.000Z',
    upstreamId: 'up-1',
    originalName: 'search_media',
  })
})

test('builds tool failure drilldown query', () => {
  assert.deepEqual(statsToolFailuresQuery(range, tool), {
    since: '2026-06-24T01:00:00.000Z',
    until: '2026-06-24T02:00:00.000Z',
    upstreamId: 'up-1',
    originalName: 'search_media',
    success: 'false',
  })
})
