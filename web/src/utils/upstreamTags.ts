/**
 * 上游标签分组/筛选纯函数。
 *
 * 与后端 manager.normalizeTags 对齐：trim、去空、大小写去重、保序。
 * 仅用于管理台组织，不参与运行时路由。
 */

export type UpstreamTagFilter = 'all' | 'untagged' | string

export interface UpstreamTagLike {
  config?: {
    tags?: string[] | null
  }
}

export interface TagCount {
  tag: string
  count: number
}

export interface UpstreamTagGroup<T extends UpstreamTagLike> {
  /** 展示用标签；未分组时为 null。 */
  tag: string | null
  key: string
  label: string
  items: T[]
}

/** 规范化标签列表：trim、去空、大小写不敏感去重、保留首次出现的书写形式。 */
export function normalizeTags(tags: readonly string[] | null | undefined): string[] {
  if (tags == null || tags.length === 0) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const tag of tags) {
    if (typeof tag !== 'string') continue
    const value = tag.trim()
    if (value === '') continue
    const key = value.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(value)
  }
  return out
}

function tagsOf(up: UpstreamTagLike): string[] {
  return normalizeTags(up.config?.tags ?? [])
}

/** 是否包含指定标签（大小写不敏感）。 */
export function matchTag(up: UpstreamTagLike, tag: string): boolean {
  const target = tag.trim().toLowerCase()
  if (target === '') return false
  return tagsOf(up).some((item) => item.toLowerCase() === target)
}

/** 是否无有效标签。 */
export function isUntagged(up: UpstreamTagLike): boolean {
  return tagsOf(up).length === 0
}

/**
 * 收集全量标签及命中次数。
 * count 按「含该标签的上游数」统计；同一上游多标签分别计数。
 */
export function collectAllTags(upstreams: readonly UpstreamTagLike[]): TagCount[] {
  const counts = new Map<string, { tag: string; count: number }>()
  for (const up of upstreams) {
    for (const tag of tagsOf(up)) {
      const key = tag.toLowerCase()
      const current = counts.get(key)
      if (current === undefined) {
        counts.set(key, { tag, count: 1 })
      } else {
        current.count += 1
      }
    }
  }
  return [...counts.values()].sort((a, b) => {
    if (b.count !== a.count) return b.count - a.count
    return a.tag.localeCompare(b.tag, 'zh-CN')
  })
}

/** 统计未分组上游数量。 */
export function countUntagged(upstreams: readonly UpstreamTagLike[]): number {
  let n = 0
  for (const up of upstreams) {
    if (isUntagged(up)) n += 1
  }
  return n
}

/**
 * 按标签筛选。
 * - all：不过滤
 * - untagged：无标签
 * - 其他：大小写不敏感命中该标签
 */
export function filterUpstreamsByTag<T extends UpstreamTagLike>(
  list: readonly T[],
  selected: UpstreamTagFilter,
): T[] {
  if (selected === 'all' || selected === '') return [...list]
  if (selected === 'untagged') return list.filter((up) => isUntagged(up))
  return list.filter((up) => matchTag(up, selected))
}

/**
 * 按标签分区：每个标签一组；无标签进「未分组」。
 * 多标签上游会出现在多个分区（仅管理视角展示，不改数据）。
 * 分区顺序：标签按 collectAllTags 顺序，未分组始终最后。
 */
export function groupUpstreamsByTag<T extends UpstreamTagLike>(
  list: readonly T[],
): UpstreamTagGroup<T>[] {
  const tagMeta = collectAllTags(list)
  const groups: UpstreamTagGroup<T>[] = tagMeta.map((item) => ({
    tag: item.tag,
    key: `tag:${item.tag.toLowerCase()}`,
    label: item.tag,
    items: list.filter((up) => matchTag(up, item.tag)),
  }))

  const untagged = list.filter((up) => isUntagged(up))
  if (untagged.length > 0) {
    groups.push({
      tag: null,
      key: 'untagged',
      label: '未分组',
      items: untagged,
    })
  }
  return groups
}
