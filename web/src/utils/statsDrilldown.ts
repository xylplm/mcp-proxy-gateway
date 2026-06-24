import type { TimeRangeQuery } from '@/api/stats'

export interface StatsToolIdentity {
  UpstreamID: string
  OriginalName: string
}

export function statsRangeQuery(range: TimeRangeQuery): Record<string, string> {
  const query: Record<string, string> = {}
  if (range.start !== undefined && range.start !== '') query.since = range.start
  if (range.end !== undefined && range.end !== '') query.until = range.end
  return query
}

export function statsToolCallsQuery(
  range: TimeRangeQuery,
  item: StatsToolIdentity,
): Record<string, string> {
  return {
    ...statsRangeQuery(range),
    upstreamId: item.UpstreamID,
    originalName: item.OriginalName,
  }
}

export function statsToolFailuresQuery(
  range: TimeRangeQuery,
  item: StatsToolIdentity,
): Record<string, string> {
  return {
    ...statsToolCallsQuery(range, item),
    success: 'false',
  }
}
