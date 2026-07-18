/**
 * 路径选择器纯函数：展示规范化、多路径文本互转、最近路径存储。
 */

const RECENT_KEY = 'mpg.pathPicker.recent.v1'
const RECENT_LIMIT = 8

export function splitPathLines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter((item) => item !== '')
}

export function joinPathLines(paths: string[]): string {
  return paths
    .map((item) => item.trim())
    .filter((item) => item !== '')
    .join('\n')
}

export function appendUniquePath(text: string, path: string): string {
  const next = path.trim()
  if (next === '') return text
  const list = splitPathLines(text)
  if (list.some((item) => item === next)) return joinPathLines(list)
  return joinPathLines([...list, next])
}

export function displayPath(path: string, sep = '/'): string {
  const raw = path.trim()
  if (raw === '') return ''
  if (sep === '\\') {
    return raw.replace(/\//g, '\\')
  }
  return raw.replace(/\\/g, '/')
}

export function parentPathHint(path: string): string {
  const raw = path.replace(/[\\/]+$/, '')
  const idx = Math.max(raw.lastIndexOf('/'), raw.lastIndexOf('\\'))
  if (idx <= 0) return ''
  return raw.slice(0, idx) || ''
}

export function loadRecentPaths(scope = 'default'): string[] {
  try {
    const raw = localStorage.getItem(RECENT_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as Record<string, string[]>
    const list = parsed[scope]
    if (!Array.isArray(list)) return []
    return list
      .filter((item) => typeof item === 'string' && item.trim() !== '')
      .slice(0, RECENT_LIMIT)
  } catch {
    return []
  }
}

export function rememberPath(path: string, scope = 'default'): string[] {
  const next = path.trim()
  if (next === '') return loadRecentPaths(scope)
  try {
    const raw = localStorage.getItem(RECENT_KEY)
    const parsed = raw ? (JSON.parse(raw) as Record<string, string[]>) : {}
    const prev = Array.isArray(parsed[scope]) ? parsed[scope] : []
    const merged = [next, ...prev.filter((item) => item !== next)].slice(0, RECENT_LIMIT)
    parsed[scope] = merged
    localStorage.setItem(RECENT_KEY, JSON.stringify(parsed))
    return merged
  } catch {
    return [next]
  }
}

export function browseRootTone(kind: string): string {
  switch (kind) {
    case 'data':
      return 'bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300'
    case 'runtime':
      return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-300'
    case 'global_file':
      return 'bg-warning-50 text-warning-800 dark:bg-warning-500/10 dark:text-warning-300'
    case 'extra':
      return 'bg-gray-100 text-gray-800 ring-1 ring-gray-200 dark:bg-white/10 dark:text-gray-200 dark:ring-white/10'
    case 'context':
      return 'bg-gray-100 text-gray-700 dark:bg-white/5 dark:text-gray-300'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400'
  }
}

export function breadcrumbParts(path: string, sepHint = '/'): string[] {
  const normalized = displayPath(path, sepHint)
  if (!normalized) return []
  if (/^[A-Za-z]:/.test(normalized)) {
    const drive = normalized.slice(0, 2)
    const rest = normalized.slice(2).replace(/^[\\/]+/, '')
    return rest === ''
      ? [drive + sepHint]
      : [drive + sepHint, ...rest.split(/[\\/]+/).filter(Boolean)]
  }
  if (normalized.startsWith('/')) {
    const rest = normalized.replace(/^\/+/, '')
    return rest === '' ? ['/'] : ['/', ...rest.split('/').filter(Boolean)]
  }
  return normalized.split(/[\\/]+/).filter(Boolean)
}
