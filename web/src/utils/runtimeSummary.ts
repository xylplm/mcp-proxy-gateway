import type { RuntimeSummary, RuntimeToolStatus } from '../api/runtime'

export function toolStatusLabel(tool: RuntimeToolStatus): string {
  return tool.available ? '可用' : '未检测到'
}

export function toolStatusTone(tool: RuntimeToolStatus): 'success' | 'warning' {
  return tool.available ? 'success' : 'warning'
}

export function summarizeToolHealth(summary: Pick<RuntimeSummary, 'availableCount' | 'missingCount' | 'tools'>): string {
  const fromTools = summary.tools?.length ?? 0
  const fromCounts = (summary.availableCount ?? 0) + (summary.missingCount ?? 0)
  const total = Math.max(fromTools, fromCounts)
  if (total <= 0) return '暂无探测项'
  if ((summary.missingCount ?? 0) <= 0) {
    return `已检测到 ${summary.availableCount ?? total} 个常用工具`
  }
  return `可用 ${summary.availableCount ?? 0} · 缺失 ${summary.missingCount ?? 0}`
}

export function stdioPolicyLabel(enabled: boolean): string {
  return enabled ? '本地 stdio 已启用' : '本地 stdio 已禁用'
}

export function formatAllowlist(list: string[] | null | undefined): string {
  if (list == null || list.length === 0) return '未配置（使用服务端默认）'
  return list.join('、')
}

/** 运行时 bin 目录展示（优先 pathPrefixes[0]，否则 runtimeDir/bin）。 */
export function runtimeBinDir(summary: Pick<RuntimeSummary, 'runtimeDir' | 'pathPrefixes'>): string {
  const prefix = summary.pathPrefixes?.find((p) => p && p.length > 0)
  if (prefix) return prefix
  const root = summary.runtimeDir?.trim()
  if (!root) return ''
  return root.endsWith('/') || root.endsWith('\\') ? `${root}bin` : `${root}/bin`
}

/** 工具缺失时是否展示卷路径引导。 */
export function shouldShowRuntimeGuide(summary: Pick<RuntimeSummary, 'missingCount' | 'runtimeDir'>): boolean {
  return (summary.missingCount ?? 0) > 0 && !!summary.runtimeDir?.trim()
}

export function runtimeGuideSteps(summary: Pick<RuntimeSummary, 'runtimeDir' | 'pathPrefixes'>): string[] {
  const bin = runtimeBinDir(summary) || `${summary.runtimeDir ?? ''}/bin`
  return [
    `将 node、npx、uv、uvx 等可执行文件放入 ${bin}`,
    '也可使用 node/bin、python/bin、uv/bin 等发行版布局',
    '重启网关进程后，回到本页点击「刷新探测」',
  ]
}
