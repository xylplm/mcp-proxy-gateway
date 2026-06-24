import type { AliasRule, FilterRule, ToolPolicyRule } from '@/api/rules'
import type { ToolDef } from '@/api/tools'
import type { Upstream } from '@/api/upstreams'
import { createOriginalNameMatcher, enabledUpstreamIDs, scopedEnabledUpstreamIDs } from './rulePreviewCore.ts'
import { cachePolicyLabel, routingStrategyLabel } from './toolPolicy.ts'

export interface RuleMatchPreviewSource {
  upstreamId: string
  upstreamName: string
  originalName: string
  exposedName: string
  status: 'visible' | 'filtered'
  filterRule?: FilterRule
  aliasRule?: AliasRule
  policyRule?: ToolPolicyRule
}

export interface RuleMatchPreviewResult {
  query: string
  summary: string
  sources: RuleMatchPreviewSource[]
  visibleCount: number
  filteredCount: number
}

export function buildRuleMatchPreview(input: {
  query: string
  upstreams: Upstream[]
  toolsByUpstream: Record<string, ToolDef[]>
  filters: FilterRule[]
  aliases: AliasRule[]
  toolPolicies: ToolPolicyRule[]
}): RuleMatchPreviewResult {
  const query = input.query.trim()
  if (query === '') {
    return emptyPreview(query, '输入工具名后预览规则命中链路')
  }

  const upstreamNameById = new Map(input.upstreams.map((up) => [up.id, up.config.name || up.id]))
  const sources: RuleMatchPreviewSource[] = []
  const filters = [...input.filters].sort((a, b) => a.sortOrder - b.sortOrder)
  const aliases = [...input.aliases].sort((a, b) => a.sortOrder - b.sortOrder)
  const policies = [...input.toolPolicies].sort((a, b) => a.sortOrder - b.sortOrder)

  for (const upstreamId of enabledUpstreamIDs(input.upstreams)) {
    const tools = input.toolsByUpstream[upstreamId] ?? []
    for (const tool of tools) {
      if (!toolMatchesQuery(tool, query)) continue
      const filterRule = firstFilterMatch(tool.originalName, upstreamId, filters, input.upstreams)
      if (filterRule !== undefined) {
        sources.push({
          upstreamId,
          upstreamName: upstreamNameById.get(upstreamId) ?? upstreamId,
          originalName: tool.originalName,
          exposedName: tool.name,
          status: 'filtered',
          filterRule,
        })
        continue
      }

      const aliasRule = firstAliasMatch(tool.originalName, upstreamId, aliases, input.upstreams)
      const exposedName = aliasRule?.targetName?.trim() || tool.name
      const policyRule = firstToolPolicyMatch(exposedName, policies)
      sources.push({
        upstreamId,
        upstreamName: upstreamNameById.get(upstreamId) ?? upstreamId,
        originalName: tool.originalName,
        exposedName,
        status: 'visible',
        aliasRule,
        policyRule,
      })
    }
  }

  const visibleCount = sources.filter((item) => item.status === 'visible').length
  const filteredCount = sources.length - visibleCount
  return {
    query,
    summary: previewSummary(query, visibleCount, filteredCount),
    sources,
    visibleCount,
    filteredCount,
  }
}

export function rulePatternLabel(rule: Pick<FilterRule | AliasRule | ToolPolicyRule, 'pattern' | 'isRegex'>): string {
  return `${rule.isRegex ? '正则' : '精确'}：${rule.pattern}`
}

export function aliasEffectLabel(rule?: AliasRule): string {
  if (rule === undefined) return '未命中别名规则'
  const hasName = (rule.targetName ?? '').trim() !== ''
  const hasDesc = (rule.targetDesc ?? '').trim() !== ''
  if (hasName && hasDesc) return `改名为 ${rule.targetName}，并重写描述`
  if (hasName) return `改名为 ${rule.targetName}`
  return '重写描述'
}

export function toolPolicyEffectLabel(rule?: ToolPolicyRule): string {
  if (rule === undefined) return '未命中工具策略'
  const tags = rule.riskTags?.length ? `，标签 ${rule.riskTags.join('、')}` : ''
  return `${routingStrategyLabel(rule.routingStrategy ?? '')}，${cachePolicyLabel(rule)}${tags}`
}

function firstFilterMatch(
  originalName: string,
  upstreamId: string,
  rules: FilterRule[],
  upstreams: Upstream[],
): FilterRule | undefined {
  return rules.find((rule) => {
    if (!rule.enabled) return false
    if (!scopedEnabledUpstreamIDs(rule, upstreams).includes(upstreamId)) return false
    const matcher = createOriginalNameMatcher(rule.pattern, rule.isRegex)
    return matcher?.(originalName) ?? false
  })
}

function firstAliasMatch(
  originalName: string,
  upstreamId: string,
  rules: AliasRule[],
  upstreams: Upstream[],
): AliasRule | undefined {
  return rules.find((rule) => {
    if (!scopedEnabledUpstreamIDs(rule, upstreams).includes(upstreamId)) return false
    const matcher = createOriginalNameMatcher(rule.pattern, rule.isRegex)
    return matcher?.(originalName) ?? false
  })
}

function firstToolPolicyMatch(exposedName: string, rules: ToolPolicyRule[]): ToolPolicyRule | undefined {
  return rules.find((rule) => {
    if (!rule.enabled) return false
    const matcher = createOriginalNameMatcher(rule.pattern, rule.isRegex)
    return matcher?.(exposedName) ?? false
  })
}

function toolMatchesQuery(tool: ToolDef, query: string): boolean {
  const q = query.toLowerCase()
  return [tool.originalName, tool.name, tool.description]
    .some((value) => (value ?? '').toLowerCase().includes(q))
}

function previewSummary(query: string, visibleCount: number, filteredCount: number): string {
  const total = visibleCount + filteredCount
  if (total === 0) return `当前缓存中未找到「${query}」`
  if (visibleCount === 0) return `找到 ${total} 个来源，均被屏蔽规则过滤`
  if (filteredCount > 0) return `找到 ${total} 个来源，${visibleCount} 个可见，${filteredCount} 个被屏蔽`
  return `找到 ${visibleCount} 个可见来源`
}

function emptyPreview(query: string, summary: string): RuleMatchPreviewResult {
  return {
    query,
    summary,
    sources: [],
    visibleCount: 0,
    filteredCount: 0,
  }
}
