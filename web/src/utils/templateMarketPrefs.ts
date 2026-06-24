export const TEMPLATE_MARKET_PREFS_STORAGE_KEY = 'mpg.templateMarketPrefs.v1'
export const TEMPLATE_MARKET_RECENT_LIMIT = 6

export interface TemplateMarketPrefs {
  favoriteIds: string[]
  recentIds: string[]
}

export type TemplateMarketViewFilter = 'all' | 'favorites' | 'recent'

export interface TemplatePreferenceItem {
  id: string
}

export const EMPTY_TEMPLATE_MARKET_PREFS: TemplateMarketPrefs = {
  favoriteIds: [],
  recentIds: [],
}

export function normalizeTemplateMarketPrefs(value: unknown): TemplateMarketPrefs {
  if (!isRecord(value)) return { ...EMPTY_TEMPLATE_MARKET_PREFS }
  return {
    favoriteIds: uniqueStringList(value.favoriteIds),
    recentIds: uniqueStringList(value.recentIds).slice(0, TEMPLATE_MARKET_RECENT_LIMIT),
  }
}

export function toggleTemplateFavorite(
  prefs: TemplateMarketPrefs,
  templateId: string,
): TemplateMarketPrefs {
  const id = templateId.trim()
  if (id === '') return normalizeTemplateMarketPrefs(prefs)
  const favoriteIds = new Set(prefs.favoriteIds)
  if (favoriteIds.has(id)) favoriteIds.delete(id)
  else favoriteIds.add(id)
  return normalizeTemplateMarketPrefs({
    favoriteIds: Array.from(favoriteIds),
    recentIds: prefs.recentIds,
  })
}

export function markTemplateRecentlyUsed(
  prefs: TemplateMarketPrefs,
  templateId: string,
  limit = TEMPLATE_MARKET_RECENT_LIMIT,
): TemplateMarketPrefs {
  const id = templateId.trim()
  if (id === '') return normalizeTemplateMarketPrefs(prefs)
  return normalizeTemplateMarketPrefs({
    favoriteIds: prefs.favoriteIds,
    recentIds: [id, ...prefs.recentIds.filter((item) => item !== id)].slice(0, Math.max(1, limit)),
  })
}

export function filterTemplatesByPreference<T extends TemplatePreferenceItem>(
  templates: T[],
  prefs: TemplateMarketPrefs,
  filter: TemplateMarketViewFilter,
): T[] {
  if (filter === 'all') return templates
  if (filter === 'favorites') {
    const favoriteIds = new Set(prefs.favoriteIds)
    return templates.filter((template) => favoriteIds.has(template.id))
  }

  const order = new Map(prefs.recentIds.map((id, index) => [id, index]))
  return templates
    .filter((template) => order.has(template.id))
    .slice()
    .sort((a, b) => (order.get(a.id) ?? 0) - (order.get(b.id) ?? 0))
}

export function loadTemplateMarketPrefs(): TemplateMarketPrefs {
  if (typeof localStorage === 'undefined') return { ...EMPTY_TEMPLATE_MARKET_PREFS }
  try {
    const raw = localStorage.getItem(TEMPLATE_MARKET_PREFS_STORAGE_KEY)
    if (raw === null || raw === '') return { ...EMPTY_TEMPLATE_MARKET_PREFS }
    return normalizeTemplateMarketPrefs(JSON.parse(raw))
  } catch {
    return { ...EMPTY_TEMPLATE_MARKET_PREFS }
  }
}

export function saveTemplateMarketPrefs(prefs: TemplateMarketPrefs): void {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(
      TEMPLATE_MARKET_PREFS_STORAGE_KEY,
      JSON.stringify(normalizeTemplateMarketPrefs(prefs)),
    )
  } catch {
    // 偏好只提升模板市场易用性，存储失败时保持内存态即可。
  }
}

function uniqueStringList(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const item of value) {
    if (typeof item !== 'string') continue
    const id = item.trim()
    if (id === '' || seen.has(id)) continue
    seen.add(id)
    out.push(id)
  }
  return out
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}
