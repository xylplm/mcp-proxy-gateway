import type { ToolDetail } from '@/api/tools'

export type ToolGovernanceTone = 'success' | 'warning' | 'info' | 'neutral'

export interface ToolGovernanceItem {
  key: string
  title: string
  description: string
  tone: ToolGovernanceTone
}

export interface ToolGovernanceSummary {
  title: string
  description: string
  items: ToolGovernanceItem[]
}

export function explainToolGovernance(detail: ToolDetail): ToolGovernanceSummary {
  const sources = detail.sources ?? []
  const compatibleSources = sources.filter((source) => source.compatible)
  const incompatibleSources = sources.filter((source) => !source.compatible)
  const rateLimitedSources = sources.filter((source) => source.rateLimits?.enabled)
  const renamedSources = sources.filter((source) => source.originalName !== detail.tool.name)
  const descriptionDiffSources = sources.filter(
    (source) => normalized(source.description) !== '' && normalized(source.description) !== normalized(detail.tool.description),
  )

  const items: ToolGovernanceItem[] = [
    sourceMergeItem(detail, sources),
    routingItem(compatibleSources, incompatibleSources),
  ]

  if (renamedSources.length > 0 || descriptionDiffSources.length > 0) {
    items.push(aliasItem(detail, renamedSources, descriptionDiffSources))
  }
  if (detail.tool.schemaConflict || incompatibleSources.length > 0) {
    items.push(schemaItem(compatibleSources.length, incompatibleSources))
  }
  if (rateLimitedSources.length > 0) {
    items.push(rateLimitItem(rateLimitedSources))
  }

  return {
    title: summaryTitle(detail, sources, incompatibleSources),
    description: summaryDescription(detail, compatibleSources, incompatibleSources),
    items,
  }
}

function sourceMergeItem(detail: ToolDetail, sources: NonNullable<ToolDetail['sources']>): ToolGovernanceItem {
  if (sources.length <= 1) {
    return {
      key: 'source',
      title: '单一来源',
      description: `${detail.tool.name} 当前只来自一个上游，调用会直接路由到该来源。`,
      tone: 'success',
    }
  }
  return {
    key: 'source',
    title: '多来源合并',
    description: `${detail.tool.name} 由 ${sources.length} 个上游来源合并为一个对外工具，网关会按当前路由策略选择可用来源。`,
    tone: 'info',
  }
}

function routingItem(
  compatibleSources: NonNullable<ToolDetail['sources']>,
  incompatibleSources: NonNullable<ToolDetail['sources']>,
): ToolGovernanceItem {
  if (compatibleSources.length === 0) {
    return {
      key: 'routing',
      title: '暂无可路由来源',
      description: '所有来源都与展示 Schema 不一致，建议刷新工具或拆分别名后再调用。',
      tone: 'warning',
    }
  }
  if (incompatibleSources.length === 0) {
    return {
      key: 'routing',
      title: '路由可用',
      description: `${compatibleSources.length} 个来源可参与调用路由。`,
      tone: 'success',
    }
  }
  return {
    key: 'routing',
    title: '部分来源跳过',
    description: `${compatibleSources.length} 个来源可参与路由，${incompatibleSources.length} 个来源因 Schema 不一致不会作为默认调用目标。`,
    tone: 'warning',
  }
}

function aliasItem(
  detail: ToolDetail,
  renamedSources: NonNullable<ToolDetail['sources']>,
  descriptionDiffSources: NonNullable<ToolDetail['sources']>,
): ToolGovernanceItem {
  const parts: string[] = []
  if (renamedSources.length > 0) {
    const originals = unique(renamedSources.map((source) => source.originalName)).slice(0, 3)
    parts.push(`对外名由 ${originals.join('、')} 映射为 ${detail.tool.name}`)
  }
  if (descriptionDiffSources.length > 0) {
    parts.push('不同来源的展示描述存在差异')
  }
  return {
    key: 'alias',
    title: '名称 / 描述已治理',
    description: `${parts.join('；')}。`,
    tone: 'info',
  }
}

function schemaItem(
  compatibleCount: number,
  incompatibleSources: NonNullable<ToolDetail['sources']>,
): ToolGovernanceItem {
  const names = unique(incompatibleSources.map((source) => source.upstreamName || source.upstreamId)).slice(0, 3)
  const suffix = incompatibleSources.length > names.length ? ` 等 ${incompatibleSources.length} 个来源` : names.join('、')
  return {
    key: 'schema',
    title: 'Schema 需要关注',
    description: `${suffix} 的入参 Schema 与当前展示版本不一致；当前仍保留 ${compatibleCount} 个可调用来源。`,
    tone: 'warning',
  }
}

function rateLimitItem(sources: NonNullable<ToolDetail['sources']>): ToolGovernanceItem {
  const names = unique(sources.map((source) => source.upstreamName || source.upstreamId)).slice(0, 3)
  const suffix = sources.length > names.length ? ` 等 ${sources.length} 个来源` : names.join('、')
  return {
    key: 'rate-limit',
    title: '上游限流参与路由',
    description: `${suffix} 已配置调用额度，命中额度时网关会尝试选择其他可用来源。`,
    tone: 'neutral',
  }
}

function summaryTitle(
  detail: ToolDetail,
  sources: NonNullable<ToolDetail['sources']>,
  incompatibleSources: NonNullable<ToolDetail['sources']>,
): string {
  if (sources.length === 0) return '缺少来源信息'
  if (incompatibleSources.length > 0 || detail.tool.schemaConflict) return '可见，但存在治理提醒'
  if (sources.length > 1) return '多来源工具，当前可正常调用'
  return '当前可正常调用'
}

function summaryDescription(
  detail: ToolDetail,
  compatibleSources: NonNullable<ToolDetail['sources']>,
  incompatibleSources: NonNullable<ToolDetail['sources']>,
): string {
  if ((detail.sources ?? []).length === 0) {
    return '当前聚合结果没有返回来源明细，可刷新工具目录后再查看。'
  }
  if (incompatibleSources.length > 0) {
    return `${compatibleSources.length} 个来源可参与调用，${incompatibleSources.length} 个来源因 Schema 差异被标记为需关注。`
  }
  return `${compatibleSources.length} 个来源可参与调用路由，当前没有发现 Schema 冲突。`
}

function unique(values: string[]): string[] {
  return Array.from(new Set(values.filter((value) => value.trim() !== '')))
}

function normalized(value: string | undefined): string {
  return (value ?? '').trim()
}
