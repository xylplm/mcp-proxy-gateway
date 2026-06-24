export type ToolCatalogConflictFilter = 'all' | 'conflict' | 'multi'
export type ToolCatalogRiskFilter = 'all' | 'risk'

export interface ToolCatalogQueryState {
  apiKeyId: string
  search: string
  upstreamId: string
  status: ToolCatalogConflictFilter
  risk: ToolCatalogRiskFilter
  tool: string
}

export type ToolCatalogQuery = Record<string, string>

const conflictFilters = new Set<ToolCatalogConflictFilter>(['all', 'conflict', 'multi'])
const riskFilters = new Set<ToolCatalogRiskFilter>(['all', 'risk'])

export function emptyToolCatalogQueryState(): ToolCatalogQueryState {
  return {
    apiKeyId: '',
    search: '',
    upstreamId: '',
    status: 'all',
    risk: 'all',
    tool: '',
  }
}

export function parseToolCatalogQuery(query: Record<string, unknown>): ToolCatalogQueryState {
  const status = firstQueryValue(query.status)
  const risk = firstQueryValue(query.risk)
  return {
    apiKeyId: firstQueryValue(query.apiKeyId),
    search: firstQueryValue(query.q),
    upstreamId: firstQueryValue(query.upstreamId),
    status: conflictFilters.has(status as ToolCatalogConflictFilter)
      ? (status as ToolCatalogConflictFilter)
      : 'all',
    risk: riskFilters.has(risk as ToolCatalogRiskFilter) ? (risk as ToolCatalogRiskFilter) : 'all',
    tool: firstQueryValue(query.tool),
  }
}

export function buildToolCatalogQuery(state: ToolCatalogQueryState): ToolCatalogQuery {
  const query: ToolCatalogQuery = {}
  const apiKeyId = state.apiKeyId.trim()
  const search = state.search.trim()
  const upstreamId = state.upstreamId.trim()
  const tool = state.tool.trim()
  if (apiKeyId !== '') query.apiKeyId = apiKeyId
  if (search !== '') query.q = search
  if (upstreamId !== '') query.upstreamId = upstreamId
  if (state.status !== 'all') query.status = state.status
  if (state.risk !== 'all') query.risk = state.risk
  if (tool !== '') query.tool = tool
  return query
}

export function sameToolCatalogQuery(left: ToolCatalogQuery, right: ToolCatalogQuery): boolean {
  const leftKeys = Object.keys(left).sort()
  const rightKeys = Object.keys(right).sort()
  if (leftKeys.length !== rightKeys.length) return false
  return leftKeys.every((key, index) => key === rightKeys[index] && left[key] === right[key])
}

function firstQueryValue(value: unknown): string {
  if (Array.isArray(value)) return firstQueryValue(value[0])
  return typeof value === 'string' ? value.trim() : ''
}
